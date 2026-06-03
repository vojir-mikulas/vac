package ws

// Topic name constructors. Producers and the WS handlers share these so a topic
// string is never spelled out in two places.

// BuildTopic carries one deployment's live build-log lines, terminated by a
// "build-end" control frame.
func BuildTopic(deploymentID string) string { return "build:" + deploymentID }

// LogsTopic carries one app's live runtime (container stdout/stderr) lines,
// tagged by service.
func LogsTopic(appID string) string { return "logs:" + appID }

// StatsTopic carries one app's per-service stats samples. Subscriber-gated: the
// stats collector runs only while this topic has subscribers.
func StatsTopic(appID string) string { return statsPrefix + appID }

const statsPrefix = "stats:"

// ParseStatsTopic extracts the app id from a stats topic, reporting whether the
// topic was a stats topic at all. Used by the stats manager's subscribe hook.
func ParseStatsTopic(topic string) (appID string, ok bool) {
	if len(topic) <= len(statsPrefix) || topic[:len(statsPrefix)] != statsPrefix {
		return "", false
	}
	return topic[len(statsPrefix):], true
}

// HostTopic carries host-level CPU/RAM/disk + aggregate request-rate samples.
const HostTopic = "host"

// DeploymentsTopic carries instance-wide deploy-queue change notifications (plan
// 20). Producers publish a payload-less "deployments" frame whenever a
// deployment is created, transitions, or settles; the queue-panel WS handler
// re-reads the active list and pushes a fresh snapshot on each one.
const DeploymentsTopic = "deployments"

// Frame type tokens.
const (
	TypeBuild       = "build"
	TypeBuildEnd    = "build-end"
	TypeLog         = "log"
	TypeStats       = "stats"
	TypeHost        = "host"
	TypeDeployments = "deployments"
)
