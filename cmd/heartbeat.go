package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

type heartbeatOptions struct {
	agent   string
	leaseID string
}

func newHeartbeatCmd(deps Dependencies) *cobra.Command {
	o := &heartbeatOptions{}
	cmd := newCommand("heartbeat <task-id>", "Renew the lease for a claimed task",
		`Renew the lease for a claimed task. This is a true lease renewal: the
expiry is extended by the full recorded lease duration, not just stamped.

Lease contract:
  - only the lease owner may renew (--agent matching the lease owner,
    --lease-id matching the lease when provided);
  - an expired lease cannot be renewed (LEASE_EXPIRED): claim the task again;
  - legacy locks (pre-lease format) cannot be renewed: release them with
    --force --reason and claim again.

Flags:
  --agent      Agent name renewing the lease
  --lease-id   Lease ID to renew (optional; verified against the lease)`)
	cmd.Args = exactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if o.agent == "" {
			return clierr.New(clierr.KindUsage, "required flag(s) \"agent\" not set")
		}

		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(deps, s, "heartbeat", o.agent, taskID, func() error {
			lock, err := service.HeartbeatTask(s, deps.Clock, taskID, o.agent, o.leaseID)
			if err != nil {
				return classifyErr("heartbeat failed", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Heartbeat sent for task %s.\n", taskID)
			fmt.Fprintf(cmd.OutOrStdout(), "Lease %s renewed: expires %s\n", lock.LeaseID, lock.ExpiresAt)
			return nil
		})
	}

	cmd.Flags().StringVar(&o.agent, "agent", "", "Agent name renewing the lease")
	cmd.Flags().StringVar(&o.leaseID, "lease-id", "", "Lease ID to renew (verified against the lease)")
	return cmd
}
