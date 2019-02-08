/*
Copyright 2018 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package update

import (
	"context"

	"github.com/gravitational/gravity/lib/fsm"
	"github.com/gravitational/gravity/lib/storage"

	"github.com/gravitational/trace"
	log "github.com/sirupsen/logrus"
)

// NewUpdatePhaseSync returns an instance of the sync phase executor
func NewUpdatePhaseSync(c FSMConfig, plan storage.OperationPlan, phase storage.OperationPhase, remote fsm.Remote, logger log.FieldLogger) (*updatePhaseSync, error) {
	if phase.Data == nil || phase.Data.Server == nil {
		return nil, trace.NotFound("no server specified for phase %q", phase.ID)
	}
	return &updatePhaseSync{
		server:      *phase.Data.Server,
		FieldLogger: logger,
		remote:      remote,
	}, nil
}

// PreCheck makes sure the phase is being executed on the correct server
func (p *updatePhaseSync) PreCheck(ctx context.Context) error {
	return trace.Wrap(p.remote.CheckServer(ctx, p.server))
}

// PostCheck is no-op for this phase
func (p *updatePhaseSync) PostCheck(context.Context) error {
	return nil
}

// Execute is a no-op for this phase
func (p *updatePhaseSync) Execute(context.Context) error {
	return nil
}

// Rollback is a no-op for this phase
func (p *updatePhaseSync) Rollback(context.Context) error {
	return nil
}

// updatePhaseSync implements the phase synchronization point.
// The purpose of this phase is to trigger plan synchronization in
// an agent running on server
type updatePhaseSync struct {
	// FieldLogger is used for logging
	log.FieldLogger
	// server is the server to execute on
	server storage.Server
	remote fsm.Remote
}
