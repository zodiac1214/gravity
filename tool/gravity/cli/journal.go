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

package cli

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/localenv"
	"github.com/gravitational/gravity/lib/pack"
	"github.com/gravitational/gravity/lib/state"
	"github.com/gravitational/gravity/lib/system"
	"github.com/gravitational/gravity/lib/system/mount"
	"github.com/gravitational/gravity/lib/utils"

	"github.com/gravitational/trace"
	"github.com/sirupsen/logrus"
)

func exportRuntimeJournal(env *localenv.LocalEnvironment, outputFile string) error {
	stateDir, err := state.GetStateDir()
	if err != nil {
		return trace.Wrap(err)
	}

	runtimePackage, err := pack.FindRuntimePackage(env.Packages)
	if err != nil {
		return trace.Wrap(err)
	}

	runtimePath, err := env.Packages.UnpackedPath(*runtimePackage)
	if err != nil {
		return trace.Wrap(err)
	}

	rootDir := filepath.Join(runtimePath, "rootfs")
	logDir := filepath.Join(state.LogDir(stateDir), "journal")
	m := mount.NewMounter(rootDir)
	if err := m.RoBindMount(logDir, journalDir); err != nil {
		return trace.Wrap(err)
	}

	cleanup := func(context.Context) error {
		if errUnmount := m.Unmount(journalDir); errUnmount != nil {
			log.Warnf("Failed to unmount %v: %v.", journalDir, errUnmount)
		}
		return nil
	}
	defer cleanup(context.TODO())

	logger := log.WithFields(logrus.Fields{
		"runtime-package": runtimePackage.String(),
		"rootfs":          rootDir,
	})
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	go utils.WatchTerminationSignals(ctx, cancel, utils.StopperFunc(cleanup), logger)

	var w io.Writer = os.Stdout
	if outputFile != "" {
		f, err := os.Create(outputFile)
		if err != nil {
			return trace.ConvertSystemError(err)
		}
		defer f.Close()
		w = f
	}

	logger.Info("Export journal.")

	zip := gzip.NewWriter(w)
	defer zip.Close()
	cmd := exec.CommandContext(ctx, utils.Exe.Path, "system", "stream-runtime-journal")
	cmd.Stdout = zip
	cmd.Stderr = zip
	if err = cmd.Run(); err != nil {
		return trace.Wrap(err)
	}

	return nil
}

func streamRuntimeJournal(env *localenv.LocalEnvironment) error {
	runtimePackage, err := pack.FindRuntimePackage(env.Packages)
	if err != nil {
		return trace.Wrap(err)
	}

	runtimePath, err := env.Packages.UnpackedPath(*runtimePackage)
	if err != nil {
		return trace.Wrap(err)
	}

	rootDir := filepath.Join(runtimePath, "rootfs")
	err = system.DropCapabilitiesForChroot()
	if err != nil {
		return trace.Wrap(err)
	}

	if err := system.Chroot(rootDir); err != nil {
		return trace.Wrap(err)
	}

	const cmd = defaults.JournalctlBin
	args := []string{
		cmd,
		"--output", "export",
		"-D", journalDir,
	}
	if err := syscall.Exec(cmd, args, nil); err != nil {
		return trace.Wrap(trace.ConvertSystemError(err),
			"failed to execve(%q, %q)", cmd, args)
	}

	return nil
}

const journalDir = "/tmp/journal"
