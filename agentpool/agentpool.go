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
	"github.com/docker/go-connections/nat"
	"github.com/fatih/color"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

//go:embed Dockerfile
var dockerfile embed.FS

//go:embed start.sh
var shScript embed.FS

// ContainerIDs the ids of the containers that are created
var ContainerIDs []string

// CreateBuildCtx - creates the Dockerfile and the start.sh script in a folder where migr8 is run
// in order to create an accessible build context
func CreateBuildCtx(path string) error {
	color.Cyan("[INFO:] CREATING DOCKERFILE AND SHELL SCRIPT REQUIRED TO BUILD THE AGENT POOL IMAGE")

	// make a new folder for the build context
	mkDirErr := os.Mkdir(path, 0777)
	if mkDirErr != nil {
		return errors.New("[ERR:] => MKDIR => " + mkDirErr.Error())
	}

	// load embedded file contents
	dockerfile, dockerfileErr := dockerfile.ReadFile("Dockerfile")
	if dockerfileErr != nil {
		return errors.New("[ERR:] => DOCKERFILE.READFILE => " + dockerfileErr.Error())
	}

	psScript, psErr := shScript.ReadFile("start.sh")
	if psErr != nil {
		return errors.New("[ERR:] => SHSCRIPT.READFILE => " + psErr.Error())
	}

	// create new files with the embedded contents to bypass cli invocation restriction
	dockerFileWriteErr := os.WriteFile(path+"/Dockerfile", dockerfile, os.ModePerm)
	if dockerFileWriteErr != nil {
		return errors.New("[ERR:] => DOCKERFILE.WRITEFILE => " + dockerFileWriteErr.Error())
	}
	psWriteErr := os.WriteFile(path+"/start.sh", psScript, os.ModePerm)
	if psWriteErr != nil {
		return errors.New("[ERR:] => SHSCRIPT.WRITEFILE => " + psWriteErr.Error())
	}
	return nil
}

// StartAgentPool - starts a new agent pool container
func StartAgentPool(details ConfigDetails) (string, error) {
	// create the container
	color.Cyan("[AGENT CONTAINER %s:] CREATING CONTAINER", details.ContainerName)

	config := getAgentPoolConfig(details)

	container, createErr := containers.CreateContainer(&config)
	if createErr != nil {
		errorMsg := fmt.Sprintf("[AGENT CONTAINER %s:] FAILED TO CREATE AGENT CONTAINER => %s", details.ContainerName, createErr.Error())
		return "", errors.New(errorMsg)
	}
	ContainerIDs = append(ContainerIDs, container.ID)
	color.Green("[AGENT CONTAINER %s:] CONTAINER CREATED SUCCESSFULLY", details.ContainerName)

	// start the container
	color.Cyan("[AGENT CONTAINER %s:] STARTING CONTAINER", details.ContainerName)
	startErr := containers.StartContainer(container)
	if startErr != nil {
		errorMsg := fmt.Sprintf("[AGENT CONTAINER %s:] FAILED TO START AGENT CONTAINER => %s", details.ContainerName, startErr.Error())
		return "", errors.New(errorMsg)
	}

	// container health check
	color.Cyan("[AGENT CONTAINER %s:] CHECKING CONTAINER HEALTH", details.ContainerName)
	isContainerHealthy := false
	for {
		isHealthy, err := isAgentPoolContainerHealthy(container.ID)
		isContainerHealthy = isHealthy
		if err == nil && !isHealthy {
			color.Yellow("[AGENT CONTAINER %s:] WAITING ON CONTAINER HEALTH CHECK", details.ContainerName)
		}
		if err != nil || isHealthy {
			break
		}
		time.Sleep(30 * time.Second)
	}

	if isContainerHealthy {
		color.Green("[AGENT CONTAINER %s:] AGENT CONTAINER STARTED SUCCESSFULLY", details.ContainerName)
	}

	if !isContainerHealthy {
		errorMsg := fmt.Sprintf("[AGENT CONTAINER %s:] CONTAINER HEALTH STATUS FAILED", details.ContainerName)
		return "", errors.New(errorMsg)
	}

	return container.ID, nil
}

func getAgentPoolConfig(details ConfigDetails) containers.ContainerCreateConfig {
	newport, err := nat.NewPort("tcp", "80")
	if err != nil {
		fmt.Println("Unable to create docker port")
	}

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
			Healthcheck: &container.HealthConfig{
				Test:        []string{"CMD", "dir"},
				Interval:    1 * time.Minute,
				Timeout:     30 * time.Second,
				StartPeriod: 15 * time.Second,
				Retries:     1000,
			},
		},
		HostConfig: &container.HostConfig{
			PortBindings: nat.PortMap{
				newport: []nat.PortBinding{
					{
						HostIP:   "0.0.0.0",
						HostPort: "80",
					},
				},
			},
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
	cmd := []string{"ps", "aux"}
	output, err := containers.Exec(containerID, cmd)
	if err != nil {
		return false, err
	}
	return strings.Contains(output, "Agent.Listener"), err
}
