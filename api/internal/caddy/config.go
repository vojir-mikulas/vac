// Package caddy is a thin transport for Caddy's Admin API. It knows only
// Caddy's JSON config shapes — no VAC domain concepts. The proxy package
// (internal/proxy) translates VAC domains/services into the Route values
// defined here and drives the Client.
//
// VAC owns the whole config: on boot it POSTs a BaseConfig to /load, then
// manipulates only the dynamic route layer via @id handles. The DB is the
// source of truth; Caddy is a rebuildable cache of it.
package caddy

// Config is the subset of Caddy's top-level config that VAC emits in its
// base load. Everything is omitempty so the marshalled JSON stays minimal.
type Config struct {
	Admin   *Admin   `json:"admin,omitempty"`
	Logging *Logging `json:"logging,omitempty"`
	Apps    *Apps    `json:"apps,omitempty"`
}

type Admin struct {
	Listen string `json:"listen,omitempty"`
}

type Logging struct {
	Logs map[string]LogCfg `json:"logs,omitempty"`
}

type LogCfg struct {
	Writer  map[string]any `json:"writer,omitempty"`
	Encoder map[string]any `json:"encoder,omitempty"`
	Include []string       `json:"include,omitempty"`
	Exclude []string       `json:"exclude,omitempty"`
	Level   string         `json:"level,omitempty"`
}

type Apps struct {
	HTTP *HTTPApp `json:"http,omitempty"`
	TLS  *TLSApp  `json:"tls,omitempty"`
}

type HTTPApp struct {
	Servers map[string]*Server `json:"servers,omitempty"`
}

type Server struct {
	Listen []string    `json:"listen,omitempty"`
	Routes []Route     `json:"routes"`
	Logs   *ServerLogs `json:"logs,omitempty"`
}

type ServerLogs struct {
	DefaultLoggerName string `json:"default_logger_name,omitempty"`
}

type TLSApp struct {
	Automation *Automation `json:"automation,omitempty"`
}

type Automation struct {
	Policies []AutomationPolicy `json:"policies,omitempty"`
	OnDemand *OnDemand          `json:"on_demand,omitempty"`
}

type AutomationPolicy struct {
	Subjects []string         `json:"subjects,omitempty"`
	OnDemand bool             `json:"on_demand,omitempty"`
	Issuers  []map[string]any `json:"issuers,omitempty"`
}

type OnDemand struct {
	Ask string `json:"ask,omitempty"`
}

// Route is one routing rule. Created/replaced/deleted by @id so VAC can
// address each rule directly without read-modify-write on the whole config.
type Route struct {
	ID     string    `json:"@id,omitempty"`
	Match  []Match   `json:"match,omitempty"`
	Handle []Handler `json:"handle,omitempty"`
}

type Match struct {
	Host []string `json:"host,omitempty"`
}

type Handler struct {
	Handler      string        `json:"handler"`
	Upstreams    []Upstream    `json:"upstreams,omitempty"`
	HealthChecks *HealthChecks `json:"health_checks,omitempty"`
}

type Upstream struct {
	Dial string `json:"dial"`
}

type HealthChecks struct {
	Active *ActiveHealthCheck `json:"active,omitempty"`
}

type ActiveHealthCheck struct {
	Path         string `json:"path,omitempty"`
	Interval     string `json:"interval,omitempty"`
	Timeout      string `json:"timeout,omitempty"`
	ExpectStatus int    `json:"expect_status,omitempty"`
}

// UpstreamStatus is one entry from GET /reverse_proxy/upstreams — the global
// pool of upstreams Caddy is currently proxying to. proxy.WaitHealthy reads
// this to gate a deploy.
type UpstreamStatus struct {
	Address     string `json:"address"`
	NumRequests int    `json:"num_requests"`
	Fails       int    `json:"fails"`
}

// BaseOptions parameterise the base config VAC loads on boot.
type BaseOptions struct {
	// AdminListen is the admin API bind. Must stay on the internal compose
	// network only — never published. Default ":2019".
	AdminListen string
	// AccessLogPath is the file the JSON access log is written to (shared
	// volume, tailed by reqmetrics).
	AccessLogPath string
	// AskURL is the on-demand-TLS ask endpoint Caddy calls before issuing a
	// certificate for an unknown host. Empty disables on-demand.
	AskURL string
	// ACMECA optionally overrides the ACME directory (e.g. Let's Encrypt
	// staging in CI). Empty uses Caddy's default (LE production).
	ACMECA string
}

// ServerName is the single HTTP server VAC manages. Route paths reference it.
const ServerName = "vac"

// BaseConfig builds the minimal config VAC loads on boot: one HTTP server on
// :80/:443 with an empty route set, JSON access logging to a file, and TLS
// automation with an on-demand ask gate.
func BaseConfig(opts BaseOptions) *Config {
	if opts.AdminListen == "" {
		opts.AdminListen = ":2019"
	}

	cfg := &Config{
		Admin: &Admin{Listen: opts.AdminListen},
		Apps: &Apps{
			HTTP: &HTTPApp{
				Servers: map[string]*Server{
					ServerName: {
						Listen: []string{":80", ":443"},
						Routes: []Route{},
					},
				},
			},
		},
	}

	if opts.AccessLogPath != "" {
		const loggerName = "vacaccess"
		cfg.Apps.HTTP.Servers[ServerName].Logs = &ServerLogs{DefaultLoggerName: loggerName}
		// Route this server's access logs to the file, and keep them out of
		// the default (stderr) logger so we don't double-log.
		cfg.Logging = &Logging{
			Logs: map[string]LogCfg{
				"default": {Exclude: []string{"http.log.access." + loggerName}},
				loggerName: {
					Writer:  map[string]any{"output": "file", "filename": opts.AccessLogPath},
					Encoder: map[string]any{"format": "json"},
					Include: []string{"http.log.access." + loggerName},
				},
			},
		}
	}

	// TLS automation: per-host certs via the default (HTTP-challenge) issuer,
	// plus an on-demand policy gated by the ask endpoint for hosts that arrive
	// dynamically. A custom ACME CA (staging) is applied to both if set.
	auto := &Automation{}
	if opts.AskURL != "" {
		auto.OnDemand = &OnDemand{Ask: opts.AskURL}
		auto.Policies = append(auto.Policies, AutomationPolicy{OnDemand: true})
	}
	if opts.ACMECA != "" {
		issuer := map[string]any{"module": "acme", "ca": opts.ACMECA}
		if len(auto.Policies) == 0 {
			auto.Policies = append(auto.Policies, AutomationPolicy{})
		}
		for i := range auto.Policies {
			auto.Policies[i].Issuers = []map[string]any{issuer}
		}
	}
	if auto.OnDemand != nil || len(auto.Policies) > 0 {
		cfg.Apps.TLS = &TLSApp{Automation: auto}
	}

	return cfg
}
