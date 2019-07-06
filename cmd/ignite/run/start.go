package run

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	meta "github.com/weaveworks/ignite/pkg/apis/meta/v1alpha1"

	"github.com/weaveworks/ignite/pkg/constants"
	"github.com/weaveworks/ignite/pkg/network/cni"
	"github.com/weaveworks/ignite/pkg/runtime/docker"
	"github.com/weaveworks/ignite/pkg/util"
	"github.com/weaveworks/ignite/pkg/version"
)

const (
	NetworkModeCNI    = "cni"
	NetworkModeBridge = "bridge"
)

var NetworkModes = []string{
	NetworkModeCNI,
	NetworkModeBridge,
}

type StartFlags struct {
	PortMappings []string
	Interactive  bool
	Debug        bool
	NetworkMode  string
}

type startOptions struct {
	*StartFlags
	*attachOptions
}

func (sf *StartFlags) NewStartOptions(vmMatch string) (*startOptions, error) {
	ao, err := NewAttachOptions(vmMatch)
	if err != nil {
		return nil, err
	}

	// Disable running check as it takes a while for the in-container Ignite to update the state
	ao.checkRunning = false

	if sf.NetworkMode != NetworkModeCNI && sf.NetworkMode != NetworkModeBridge {
		return nil, fmt.Errorf("invalid network mode %s, must be one of %v", sf.NetworkMode, NetworkModes)
	}

	return &startOptions{sf, ao}, nil
}

func Start(so *startOptions) error {
	// Check if the given VM is already running
	if so.vm.Running() {
		return fmt.Errorf("VM %q is already running", so.vm.GetUID())
	}

	// Setup the snapshot overlay filesystem
	if err := so.vm.SetupSnapshot(); err != nil {
		return err
	}

	// Resolve the Ignite binary to be mounted inside the container
	path, err := exec.LookPath(os.Args[0])
	if err != nil {
		return err
	}
	igniteBinary, _ := filepath.Abs(path)

	vmDir := filepath.Join(constants.VM_DIR, so.vm.GetUID().String())
	kernelDir := filepath.Join(constants.KERNEL_DIR, so.vm.Spec.Kernel.UID.String())

	dockerArgs := []string{
		"-itd",
		fmt.Sprintf("--label=ignite.name=%s", so.vm.GetName()),
		fmt.Sprintf("--name=%s", constants.IGNITE_PREFIX+so.vm.GetUID()),
		fmt.Sprintf("--volume=%s:/ignite/ignite", igniteBinary),
		fmt.Sprintf("--volume=%s:%s", vmDir, vmDir),
		fmt.Sprintf("--volume=%s:%s", kernelDir, kernelDir),
		fmt.Sprintf("--stop-timeout=%d", constants.STOP_TIMEOUT+constants.IGNITE_TIMEOUT),
		"--cap-add=SYS_ADMIN",          // Needed to run "dmsetup remove" inside the container
		"--cap-add=NET_ADMIN",          // Needed for removing the IP from the container's interface
		"--device=/dev/mapper/control", // This enables containerized Ignite to remove its own dm snapshot
		"--device=/dev/net/tun",        // Needed for creating TAP adapters
		"--device=/dev/kvm",            // Pass though virtualization support
		fmt.Sprintf("--device=%s", so.vm.SnapshotDev()),
	}

	if so.NetworkMode == NetworkModeCNI {
		dockerArgs = append(dockerArgs, "--net=none")
	}

	dockerCmd := append(make([]string, 0, len(dockerArgs)+2), "run")

	// If we're not debugging, remove the container post-run
	if !so.Debug {
		dockerCmd = append(dockerCmd, "--rm")
	}

	// Parse the given port mappings
	if so.vm.Spec.Ports, err = meta.ParsePortMappings(so.PortMappings); err != nil {
		return err
	}

	// Add the port mappings to Docker
	for _, portMapping := range so.vm.Spec.Ports {
		dockerArgs = append(dockerArgs, fmt.Sprintf("-p=%d:%d", portMapping.HostPort, portMapping.VMPort))
	}

	// Save the port mappings into the VM metadata
	if err := so.vm.Save(); err != nil {
		return err
	}

	// Use the :dev image tag for non-release builds
	imageTag := version.GetIgnite().GitVersion
	if version.GetIgnite().GitTreeState == "dirty" {
		imageTag = "dev"
	}
	dockerArgs = append(dockerArgs, fmt.Sprintf("weaveworks/ignite:%s", imageTag))
	dockerArgs = append(dockerArgs, so.vm.GetUID().String())

	// Create the VM container in docker
	containerID, err := util.ExecuteCommand("docker", append(dockerCmd, dockerArgs...)...)
	if err != nil {
		return fmt.Errorf("failed to start container for VM %q: %v", so.vm.GetUID(), err)
	}

	if so.NetworkMode == NetworkModeCNI {
		if err := setupCNINetworking(containerID); err != nil {
			return err
		}
		log.Printf("Networking is now handled by CNI")
	}

	log.Printf("Started Firecracker in a Docker container with ID %q", containerID)

	// If starting interactively, attach after starting
	if so.Interactive {
		return Attach(so.attachOptions)
	}
	return nil
}

func setupCNINetworking(containerID string) error {
	// TODO: Both the client and networkPlugin variables should be constructed once,
	// and accessible throughout the program.
	// TODO: Right now IP addresses aren't reclaimed when the VM is removed.
	// networkPlugin.RemoveContainerNetwork need to be called when removing the VM.
	client, err := docker.GetDockerClient()
	if err != nil {
		return err
	}
	networkPlugin, err := cni.GetCNINetworkPlugin(client)
	if err != nil {
		return err
	}
	return networkPlugin.SetupContainerNetwork(containerID)
}
