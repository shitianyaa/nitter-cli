package pipeline

// TypedErrorEnvelope is the typed data payload of an in-place error envelope
// (kind "error"): which command, at which stage, with which stable error
// code, and a human-readable message. Field names are part of the
// nitter.pipeline/v1 contract (additive-only).
type TypedErrorEnvelope struct {
	Command string `json:"command"`
	Stage   string `json:"stage"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
