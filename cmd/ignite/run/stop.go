package run

import (
	"fmt"

	"github.com/weaveworks/ignite/pkg/client"
	"github.com/weaveworks/ignite/pkg/filter"

	"github.com/weaveworks/ignite/pkg/constants"
	"github.com/weaveworks/ignite/pkg/metadata/vmmd"
	"github.com/weaveworks/ignite/pkg/util"
)

var (
	stopArgs = []string{"stop"}
	killArgs = []string{"kill", "-s", "SIGQUIT"}
)

type StopFlags struct {
	Kill bool
}

type stopOptions struct {
	*StopFlags
	vms    []*vmmd.VM
	silent bool
}

func (sf *StopFlags) NewStopOptions(vmMatches []string) (*stopOptions, error) {
	so := &stopOptions{StopFlags: sf}

	for _, match := range vmMatches {
		if vm, err := client.VMs().Find(filter.NewIDNameFilter(match)); err == nil {
			so.vms = append(so.vms, &vmmd.VM{vm})
		} else {
			return nil, err
		}
	}

	return so, nil
}

func Stop(so *stopOptions) error {
	for _, vm := range so.vms {
		// Check if the VM is running
		if !vm.Running() {
			return fmt.Errorf("VM %q is not running", vm.GetUID())
		}

		dockerArgs := stopArgs

		// Change to kill arguments if requested
		if so.Kill {
			dockerArgs = killArgs
		}

		dockerArgs = append(dockerArgs, constants.IGNITE_PREFIX+vm.GetUID().String())

		// Stop/Kill the VM in docker
		if _, err := util.ExecuteCommand("docker", dockerArgs...); err != nil {
			return fmt.Errorf("failed to stop container for VM %q: %v", vm.GetUID(), err)
		}

		if so.silent {
			continue
		}

		fmt.Println(vm.GetUID())
	}
	return nil
}
