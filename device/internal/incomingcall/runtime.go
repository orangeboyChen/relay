package incomingcall

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sim-deck-device/internal/call"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/slotpolicy"
)

// Remote provides authenticated recipient discovery and opaque call delivery.
type Remote interface {
	ListInstallationRecipients(context.Context, string, string) ([]Recipient, error)
	UploadIncomingCall(context.Context, string, string, Notification) error
}

// EventRegistrar binds later modem transitions to the generated call secret.
type EventRegistrar interface {
	RegisterIncoming(string, []byte) error
	Unregister(string)
}

// CallRejecter rejects a ringing or waiting cellular call.
type CallRejecter interface {
	Reject(context.Context) error
}

// Policy provides the current owner controls for this physical slot.
type Policy interface {
	Current() slotpolicy.Settings
}

// Runtime turns incoming modem transitions into recipient-encrypted notifications.
type Runtime struct {
	remote        Remote
	identity      identity.State
	snapshots     <-chan call.Snapshot
	events        EventRegistrar
	calls         CallRejecter
	policy        Policy
	builder       *Builder
	activeCallID  string
	pending       *pendingCall
	retryInterval time.Duration
}

type pendingCall struct {
	snapshot     call.Snapshot
	notification Notification
	callContext  CallContext
}

const deliveryRetryInterval = time.Second

// NewRuntime creates an incoming call notification runtime for one modem.
func NewRuntime(remote Remote, state identity.State, snapshots <-chan call.Snapshot, events EventRegistrar, calls CallRejecter, policy Policy) (*Runtime, error) {
	if remote == nil || snapshots == nil || events == nil || calls == nil || policy == nil || state.Credential == "" {
		return nil, errors.New("incoming-call runtime requires a bound identity, remote, snapshots, event registrar, call controller, and slot policy")
	}
	builder, err := NewBuilder(state)
	if err != nil {
		return nil, err
	}
	return &Runtime{
		remote: remote, identity: state, snapshots: snapshots, events: events, calls: calls, policy: policy, builder: builder,
		retryInterval: deliveryRetryInterval,
	}, nil
}

// Run processes call-state transitions and retries pending call delivery until cancellation.
func (runtime *Runtime) Run(ctx context.Context) error {
	ticker := time.NewTicker(runtime.retryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case snapshot, ok := <-runtime.snapshots:
			if !ok {
				return errors.New("incoming-call snapshot stream closed")
			}
			runtime.acceptSnapshot(snapshot)
			if err := runtime.deliverPending(ctx); err != nil {
				slog.Warn("incoming call notification delivery deferred", "error", err)
			}
		case <-ticker.C:
			if err := runtime.deliverPending(ctx); err != nil {
				slog.Warn("incoming call notification retry deferred", "error", err)
			}
		}
	}
}

func (runtime *Runtime) acceptSnapshot(snapshot call.Snapshot) {
	if snapshot.State == call.StateEnded {
		runtime.pending = nil
		if runtime.activeCallID != "" {
			runtime.events.Unregister(runtime.activeCallID)
			runtime.activeCallID = ""
		}
		return
	}
	if runtime.activeCallID != "" || runtime.pending != nil ||
		(snapshot.State != call.StateRinging && snapshot.State != call.StateWaiting) ||
		snapshot.IncomingNumber == "" {
		return
	}
	runtime.pending = &pendingCall{snapshot: snapshot}
}

func (runtime *Runtime) deliverPending(ctx context.Context) error {
	pending := runtime.pending
	if pending == nil {
		return nil
	}
	if !runtime.policy.Current().IncomingCallsEnabled {
		if err := runtime.calls.Reject(ctx); err != nil {
			return fmt.Errorf("reject disabled incoming call: %w", err)
		}
		runtime.pending = nil
		slog.Info("incoming call rejected by slot policy")
		return nil
	}
	if pending.notification.ID == "" {
		recipients, err := runtime.remote.ListInstallationRecipients(ctx, runtime.identity.DeviceUUID, runtime.identity.Credential)
		if err != nil {
			return fmt.Errorf("list incoming call recipients: %w", err)
		}
		if len(recipients) == 0 {
			return nil
		}
		pending.notification, pending.callContext, err = runtime.builder.Seal(recipients, pending.snapshot)
		if err != nil {
			return fmt.Errorf("encrypt incoming call: %w", err)
		}
	}
	if err := runtime.remote.UploadIncomingCall(ctx, runtime.identity.DeviceUUID, runtime.identity.Credential, pending.notification); err != nil {
		return fmt.Errorf("upload incoming call: %w", err)
	}
	if err := runtime.events.RegisterIncoming(pending.callContext.CallID, pending.callContext.SharedSecret); err != nil {
		return fmt.Errorf("register incoming call events: %w", err)
	}
	runtime.activeCallID = pending.callContext.CallID
	runtime.pending = nil
	return nil
}

func (runtime *Runtime) process(ctx context.Context, snapshot call.Snapshot) error {
	if snapshot.State == call.StateEnded {
		if runtime.activeCallID != "" {
			runtime.events.Unregister(runtime.activeCallID)
			runtime.activeCallID = ""
		}
		return nil
	}
	if runtime.activeCallID != "" ||
		(snapshot.State != call.StateRinging && snapshot.State != call.StateWaiting) ||
		snapshot.IncomingNumber == "" {
		return nil
	}
	if !runtime.policy.Current().IncomingCallsEnabled {
		if err := runtime.calls.Reject(ctx); err != nil {
			return fmt.Errorf("reject disabled incoming call: %w", err)
		}
		return nil
	}
	recipients, err := runtime.remote.ListInstallationRecipients(ctx, runtime.identity.DeviceUUID, runtime.identity.Credential)
	if err != nil {
		return fmt.Errorf("list incoming call recipients: %w", err)
	}
	if len(recipients) == 0 {
		return nil
	}
	notification, callContext, err := runtime.builder.Seal(recipients, snapshot)
	if err != nil {
		return fmt.Errorf("encrypt incoming call: %w", err)
	}
	if err := runtime.remote.UploadIncomingCall(ctx, runtime.identity.DeviceUUID, runtime.identity.Credential, notification); err != nil {
		return fmt.Errorf("upload incoming call: %w", err)
	}
	if err := runtime.events.RegisterIncoming(callContext.CallID, callContext.SharedSecret); err != nil {
		return fmt.Errorf("register incoming call events: %w", err)
	}
	runtime.activeCallID = callContext.CallID
	return nil
}
