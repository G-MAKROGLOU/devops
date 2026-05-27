package agentpool

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/G-MAKROGLOU/containers"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/fatih/color"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// Bug fix: rename embed FS vars so they don't shadow the local byte-slice
// variables created by ReadFile inside CreateBuildCtx.
//
//go:embed Dockerfile
var dockerfileFS embed.FS

//go:embed start.sh
var shScriptFS embed.FS

// ContainerIDs holds the IDs of every agent container created in this run.
var ContainerIDs []string

// CreateBuildCtx writes the embedded Dockerfile and start.sh into a local
// directory so they can be used as a Docker build context.
func CreateBuildCtx(path string) error {
	color.Cyan("[INFO:] CREATING DOCKERFILE AND SHELL SCRIPT REQUIRED TO BUILD THE AGENT POOL IMAGE")

	if err := os.Mkdir(path, 0777); err != nil {
		return errors.New("[ERR:] => MKDIR => " + err.Error())
	}

	dockerfile, err := dockerfileFS.ReadFile("Dockerfile")
	if err != nil {
		return errors.New("[ERR:] => DOCKERFILE.READFILE => " + err.Error())
	}

	psScript, err := shScriptFS.ReadFile("start.sh")
	if err != nil {
		return errors.New("[ERR:] => SHSCRIPT.READFILE => " + err.Error())
	}

	if err := os.WriteFile(path+"/Dockerfile", dockerfile, os.ModePerm); err != nil {
		return errors.New("[ERR:] => DOCKERFILE.WRITEFILE => " + err.Error())
	}
	if err := os.WriteFile(path+"/start.sh", psScript, os.ModePerm); err != nil {
		return errors.New("[ERR:] => SHSCRIPT.WRITEFILE => " + err.Error())
	}
	return nil
}

// StartAgentPool creates, starts, and health-checks a single agent container.
func StartAgentPool(details ConfigDetails) (string, error) {
	color.Cyan("[AGENT CONTAINER %s:] CREATING CONTAINER", details.ContainerName)

	config := getAgentPoolConfig(details)

	cont, createErr := containers.CreateContainer(&config)
	if createErr != nil {
		return "", fmt.Errorf("[AGENT CONTAINER %s:] FAILED TO CREATE AGENT CONTAINER => %s", details.ContainerName, createErr.Error())
	}
	ContainerIDs = append(ContainerIDs, cont.ID)
	color.Green("[AGENT CONTAINER %s:] CONTAINER CREATED SUCCESSFULLY", details.ContainerName)

	color.Cyan("[AGENT CONTAINER %s:] STARTING CONTAINER", details.ContainerName)
	if err := containers.StartContainer(cont); err != nil {
		return "", fmt.Errorf("[AGENT CONTAINER %s:] FAILED TO START AGENT CONTAINER => %s", details.ContainerName, err.Error())
	}

	color.Cyan("[AGENT CONTAINER %s:] WAITING FOR AZURE PIPELINES AGENT TO COME ONLINE", details.ContainerName)
	for {
		healthy, err := isAgentPoolContainerHealthy(cont.ID)
		if err != nil {
			return "", fmt.Errorf("[AGENT CONTAINER %s:] CONTAINER HEALTH CHECK FAILED => %s", details.ContainerName, err.Error())
		}
		if healthy {
			break
		}
		color.Yellow("[AGENT CONTAINER %s:] WAITING ON AGENT LISTENER — RETRYING IN 30s", details.ContainerName)
		time.Sleep(30 * time.Second)
	}

	color.Green("[AGENT CONTAINER %s:] AGENT CONTAINER STARTED SUCCESSFULLY", details.ContainerName)
	return cont.ID, nil
}

func getAgentPoolConfig(details ConfigDetails) containers.ContainerCreateConfig {
	env := []string{
		"AZP_URL=" + details.Org,
		"AZP_TOKEN=" + details.Pat,
		"AZP_POOL=" + details.Pool,
		"AZP_AGENT_NAME=" + details.ContainerName,
	}

	return containers.ContainerCreateConfig{
		Name: details.ContainerName,
		Config: &container.Config{
			Image: "azp_agent",
			Env:   env,
			// Bug fix: the healthcheck now matches the actual readiness check used by
			// isAgentPoolContainerHealthy. Previously it ran "dir" (always passes) which
			// made Docker's health status meaningless.
			Healthcheck: &container.HealthConfig{
				Test:        []string{"CMD-SHELL", "ps aux | grep -q '[A]gent.Listener' || exit 1"},
				Interval:    30 * time.Second,
				Timeout:     10 * time.Second,
				StartPeriod: 2 * time.Minute,
				Retries:     10,
			},
		},
		HostConfig: &container.HostConfig{
			// Bug fix: removed PortBindings. All containers were binding to host port 80
			// which caused containers 2..N to fail with "port already allocated" when
			// deploying more than one app. Azure Pipelines agents use outbound connections
			// to Azure DevOps — no host port binding is needed.
			RestartPolicy: container.RestartPolicy{
				Name: "no",
			},
			LogConfig: container.LogConfig{
				Type:   "json-file",
				Config: map[string]string{},
			},
		},
		NetworkingConfig: &network.NetworkingConfig{},
		Platform:         &v1.Platform{},
	}
}

func isAgentPoolContainerHealthy(containerID string) (bool, error) {
	output, err := containers.Exec(containerID, []string{"ps", "aux"})
	if err != nil {
		return false, err
	}
	return strings.Contains(output, "Agent.Listener"), nil
}
