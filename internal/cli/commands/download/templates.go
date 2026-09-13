package download

// Naming templates for `nitter download` (plan M10 Task 5): the
// filename_template config key (overridable per invocation with
// --filename-template) and the directory_template config key render each
// planned file's on-disk location.
//
// Semantics:
//   - filename_template (default "{id}-{seq}") renders the file name of every
//     regular planned file. With the default the produced names are
//     byte-identical to the pre-template behavior (<id>-<seq>.<ext>, covers
//     <id>-cover.<ext>). Placeholders: {id} the status id, {seq} the planned
//     file's 1-based sequence, {user} the ref's user segment AS GIVEN (empty
//     for a bare ID; the user-less /i/status/<id> route reports "i"), {kind}
//     image/video/gif/cover, {ext} the planned extension with its leading
//     dot. {ext} renders empty when the plan carries none — the response's
//     Content-Type then decides the extension at download time, appended
//     after the final rendered name exactly as without a template (R-M9-4);
//     a template without {ext} gets the (possibly empty) extension appended
//     at the end. Covers are the Kind exception BY DESIGN: they ignore the
//     filename template and always land as <id>-cover.<ext>.
//   - directory_template (default EMPTY = flat in the output directory root)
//     renders the subdirectory below the output directory per planned file,
//     from {id}/{user}/{kind}; {seq} and {ext} are forbidden in the directory
//     position. "/" separates levels; empty levels are dropped, so an empty
//     {user} contribution simply disappears. The directory is created on
//     demand (mkdir -p), exactly like the output root.
//   - An invalid template never fails the run (pixiv semantics: warn, don't
//     fail): an unknown or malformed placeholder, a forbidden placeholder in
//     the directory position, or a path separator in the filename position
//     prints ONE stderr warning line per run and falls back to the default
//     template (filenames) or flat (directory). An empty template value IS
//     the default, without a warning.
//   - Rendered names are sanitized: characters illegal in Windows filenames
//     (\ / : * ? " < > | and control characters) are replaced with "_", so a
//     rendered name cannot escape the output directory. A directory level
//     that renders to "." or ".." (a traversal attempt, from the literal
//     template or a crafted user segment) is rejected with the same warning
//     fallback.
//   - Two planned files of ONE ref rendering the same name collide: the later
//     one gets a numeric "-2", "-3", ... suffix (before the extension when
//     the name ends with it, at the end otherwise) plus a stderr warning.
//     Across refs the pre-template --on-exists semantics apply unchanged
//     (duplicate refs meet the same filenames and the exists mode decides).

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
)

// defaultFilenameTemplate is the filename template whose rendering is
// byte-identical to the pre-template naming.
const defaultFilenameTemplate = "{id}-{seq}"

// filenamePlaceholders / directoryPlaceholders are the placeholders each
// template position accepts; anything else invalidates the whole template.
var (
	filenamePlaceholders  = map[string]bool{"id": true, "seq": true, "user": true, "kind": true, "ext": true}
	directoryPlaceholders = map[string]bool{"id": true, "user": true, "kind": true}
)

// nameEngine renders one run's templates and owns its one-shot warnings. The
// engine is created once per runBatch; per-ref collision tracking lives in
// the refNamer scopes it hands out.
type nameEngine struct {
	filename  string    // effective filename template, never empty
	directory string    // effective directory template, "" = flat
	warn      io.Writer // stderr for the warning lines; nil writes nothing

	filenameWarned  bool // render-time fallback warned once
	directoryWarned bool
}

// newNameEngine validates both templates up front: an invalid one — or an
// empty filename, which IS the default — is resolved here, so every later
// render is deterministic and the warning prints once per run.
func newNameEngine(filenameTemplate, directoryTemplate string, warn io.Writer) *nameEngine {
	e := &nameEngine{warn: warn}
	switch {
	case filenameTemplate == "":
		e.filename = defaultFilenameTemplate
	case strings.ContainsAny(filenameTemplate, `/\`):
		e.warnFilenameInvalid(filenameTemplate, errors.New("template contains a path separator"))
		e.filename = defaultFilenameTemplate
	default:
		if err := validateTemplate(filenameTemplate, filenamePlaceholders); err != nil {
			e.warnFilenameInvalid(filenameTemplate, err)
			e.filename = defaultFilenameTemplate
		} else {
			e.filename = filenameTemplate
		}
	}
	switch {
	case directoryTemplate == "":
		e.directory = ""
	case strings.Contains(directoryTemplate, `\`):
		e.warnDirectoryInvalid(directoryTemplate, errors.New("template contains a backslash path separator"))
	default:
		if err := validateTemplate(directoryTemplate, directoryPlaceholders); err != nil {
			e.warnDirectoryInvalid(directoryTemplate, err)
		} else if forbiddenDirectorySegments(directoryTemplate) {
			e.warnDirectoryInvalid(directoryTemplate, errors.New(`template contains a "." or ".." path segment`))
		} else {
			e.directory = directoryTemplate
		}
	}
	return e
}

// warnf prints one stderr warning line; a nil writer and write errors are
// ignored like every other stderr diagnostic.
func (e *nameEngine) warnf(format string, args ...any) {
	if e.warn == nil {
		return
	}
	fmt.Fprintf(e.warn, "warning: download: "+format+"\n", args...)
}

func (e *nameEngine) warnFilenameInvalid(tmpl string, err error) {
	e.warnf("invalid filename_template %q (%v); using the default %q", tmpl, err, defaultFilenameTemplate)
}

func (e *nameEngine) warnDirectoryInvalid(tmpl string, err error) {
	e.warnf("invalid directory_template %q (%v); downloading flat into the output directory", tmpl, err)
}

// warnOnceFilename / warnOnceDirectory print the render-time fallback
// warnings once per run (structural invalidity is caught by the constructor;
// these cover value-dependent render failures such as an empty name or a
// traversal segment crafted through a placeholder value).
func (e *nameEngine) warnOnceFilename(tmpl, reason string) {
	if e.filenameWarned {
		return
	}
	e.filenameWarned = true
	e.warnf("filename_template %q %s; using the default %q", tmpl, reason, defaultFilenameTemplate)
}

func (e *nameEngine) warnOnceDirectory(tmpl, reason string) {
	if e.directoryWarned {
		return
	}
	e.directoryWarned = true
	e.warnf("directory_template %q %s; downloading flat into the output directory", tmpl, reason)
}

// filenameFor renders one planned file's name and its extension: the
// extension is the sanitized planned one, "" when the response's
// Content-Type decides at download time (extKnown in the fileTarget is then
// false). The name carries the extension in place when the template
// positions it with {ext}, at the end otherwise.
func (e *nameEngine) filenameFor(pf client.PlannedFile, user string) (name, ext string) {
	ext = sanitizeName(pf.Ext)
	if pf.Kind == "cover" {
		// The cover naming invariant: <id>-cover.<ext>, template or not.
		return pf.StatusID + "-cover" + ext, ext
	}
	vals := templateValues(pf, user, ext)
	render := func(tmpl string) string {
		rendered := renderTemplate(tmpl, vals)
		if !strings.Contains(tmpl, "{ext}") {
			rendered += ext
		}
		return rendered
	}
	name = render(e.filename)
	if name == "" {
		// An empty render cannot name a file (e.g. a template of only empty
		// values on the auto path): the default does, with one warning.
		e.warnOnceFilename(e.filename, "rendered an empty name")
		name = render(defaultFilenameTemplate)
	}
	return sanitizeName(name), ext
}

// templateValues builds the placeholder values of one planned file. The
// free-form values (user, ext) are sanitized; id/seq/kind are clean by
// construction (numeric ID, 1-based sequence, fixed kind set).
func templateValues(pf client.PlannedFile, user, ext string) map[string]string {
	return map[string]string{
		"id":   pf.StatusID,
		"seq":  pf.Seq,
		"user": sanitizeName(user),
		"kind": pf.Kind,
		"ext":  ext,
	}
}

// directoryFor renders the planned file's directory below outDir and creates
// it on demand. Flat (outDir itself) when no template is in effect; a
// render-time forbidden segment latches the run flat with the one-shot
// warning. The returned error is the planned file's entry error.
func (e *nameEngine) directoryFor(pf client.PlannedFile, user, outDir string) (string, error) {
	if e.directory == "" {
		return outDir, nil
	}
	rendered := renderTemplate(e.directory, templateValues(pf, user, sanitizeName(pf.Ext)))
	parts := make([]string, 0, strings.Count(rendered, "/")+1)
	for _, seg := range strings.Split(rendered, "/") {
		if seg == "" {
			continue // an empty level is dropped (an empty {user} contribution)
		}
		if seg == "." || seg == ".." {
			// Traversal attempt: values are sanitized (no separators) but
			// dots survive, so a crafted user segment can render ".." — the
			// render is rejected and the run latches flat.
			e.warnOnceDirectory(e.directory, "rendered a forbidden path segment")
			e.directory = ""
			return outDir, nil
		}
		parts = append(parts, sanitizeName(seg))
	}
	if len(parts) == 0 {
		return outDir, nil
	}
	dir := filepath.Join(outDir, filepath.Join(parts...))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create download directory %s: %w", dir, err)
	}
	return dir, nil
}

// forbiddenDirectorySegments reports whether the raw template's literal text
// already carries a whole "." or ".." level. Values cannot produce one
// (sanitized), but the post-render check runs again as defense in depth.
func forbiddenDirectorySegments(tmpl string) bool {
	for _, seg := range strings.Split(tmpl, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

// validateTemplate checks every {...} span of the template against the
// allowed placeholders; an unclosed brace or an unknown name is an error
// naming the problem (the whole template is invalid, pixiv semantics).
func validateTemplate(tmpl string, allowed map[string]bool) error {
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] != '{' {
			continue
		}
		end := strings.IndexByte(tmpl[i:], '}')
		if end < 0 {
			return errors.New(`placeholder opened with "{" but never closed`)
		}
		name := tmpl[i+1 : i+end]
		if !allowed[name] {
			return fmt.Errorf("unknown placeholder {%s}", name)
		}
		i += end
	}
	return nil
}

// renderTemplate substitutes every allowed {...} placeholder with its value;
// literal text (including stray closing braces) passes through unchanged.
// Callers validate the template first — an unclosed brace renders literally.
func renderTemplate(tmpl string, values map[string]string) string {
	var b strings.Builder
	b.Grow(len(tmpl))
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] != '{' {
			b.WriteByte(tmpl[i])
			continue
		}
		end := strings.IndexByte(tmpl[i:], '}')
		if end < 0 {
			b.WriteByte(tmpl[i])
			continue
		}
		if v, ok := values[tmpl[i+1:i+end]]; ok {
			b.WriteString(v)
			i += end
			continue
		}
		b.WriteByte(tmpl[i])
	}
	return b.String()
}

// sanitizeName replaces every character illegal in a Windows filename with
// "_": the reserved separators and wildcards (\ / : * ? " < > |) plus ASCII
// control characters. Legal characters — including Unicode — pass through.
func sanitizeName(s string) string {
	if !strings.ContainsFunc(s, isIllegalNameRune) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isIllegalNameRune(r) {
			return '_'
		}
		return r
	}, s)
}

func isIllegalNameRune(r rune) bool {
	switch {
	case r < 0x20 || r == 0x7f:
		return true
	case strings.ContainsRune(`\/:*?"<>|`, r):
		return true
	}
	return false
}

// fileTarget is the resolved on-disk destination of one planned file: the
// absolute directory (created on demand) and the name. With extKnown the
// name carries the extension in place (FetchToFile); without it the name is
// the extension-less base path whose extension the response's Content-Type
// decides (FetchToFileAuto, ruling R-M9-4) — appended after the FINAL
// rendered name, exactly as without a template.
type fileTarget struct {
	dir      string
	name     string
	extKnown bool
}

// refNamer allocates the on-disk targets of ONE ref's planned files,
// suffixing rendered-name collisions within the ref.
type refNamer struct {
	eng  *nameEngine
	used map[string]bool // rendered names already planned within this ref
}

// newRefNamer starts a fresh per-ref allocation scope.
func (e *nameEngine) newRefNamer() *refNamer {
	return &refNamer{eng: e, used: make(map[string]bool)}
}

// target resolves one planned file's directory and name, suffixing a
// collision with the names already planned for this ref. A directory
// creation failure is returned for the caller to report as the entry error.
func (n *refNamer) target(pf client.PlannedFile, user, outDir string) (fileTarget, error) {
	dir, err := n.eng.directoryFor(pf, user, outDir)
	if err != nil {
		return fileTarget{}, err
	}
	name, ext := n.eng.filenameFor(pf, user)
	if n.used[name] {
		original := name
		for i := 2; ; i++ {
			candidate := withCollisionSuffix(original, ext, i)
			if !n.used[candidate] {
				n.eng.warnCollision(original, candidate)
				name = candidate
				break
			}
		}
	}
	n.used[name] = true
	return fileTarget{dir: dir, name: name, extKnown: ext != ""}, nil
}

// withCollisionSuffix inserts the numeric suffix BEFORE the extension when
// the name ends with it, at the end otherwise (the extension-less auto path
// gets the Content-Type-derived extension after the suffix).
func withCollisionSuffix(name, ext string, n int) string {
	if ext != "" && strings.HasSuffix(name, ext) {
		return name[:len(name)-len(ext)] + "-" + strconv.Itoa(n) + ext
	}
	return name + "-" + strconv.Itoa(n)
}

func (e *nameEngine) warnCollision(original, candidate string) {
	e.warnf("planned file name %q is already used for this ref; using %q instead", original, candidate)
}
