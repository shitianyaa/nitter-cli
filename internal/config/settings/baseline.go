package settings

// DefaultConfigTOML is the baseline config.toml published on first run via
// paths.EnsureDefaultConfigFile. The active scalar keys carry the defaults
// verbatim, while the [[instances]] / [[watch.sources]] examples stay fully
// commented out: nothing is enabled by default, so a fresh install behaves
// exactly like a missing config file (pure Defaults()).
const DefaultConfigTOML = `# nitter-cli configuration (~/.nitter-cli/config.toml)
# Scalar keys below carry the defaults; edit them or use "nitter config set".
# The [[instances]] and [[watch.sources]] examples at the bottom are commented
# out on purpose: nothing is fetched until you enable them by hand.

default_limit     = 20        # tweets per manual command
max_pages         = 5         # pagination cap per fetch
request_interval  = "1s"      # global minimum request interval
retry_attempts    = 2
retry_delay       = "1s"
instance_cooldown = "60s"     # cooldown after an instance failure
proxy             = ""        # empty = use HTTPS_PROXY/ALL_PROXY env
log_level         = "info"    # debug|info
log_format        = "text"    # text|json

# Array tables below are managed by hand-editing this file ("nitter config
# set" refuses them). Examples:

# [[instances]]
# url = "http://nitter.internal:8080"
# username = ""
# password = ""

# [[watch.sources]]
# id = "user:NASA"
`
