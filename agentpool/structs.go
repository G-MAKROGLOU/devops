package agentpool

// ConfigDetails holds the details of the agent config instead of passing multiple params
type ConfigDetails struct {
	Pat           string
	Org           string
	Pool          string
	ContainerName string
}
