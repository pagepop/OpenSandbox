// Copyright 2026 Alibaba Group Holding Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alibaba/opensandbox/execd/pkg/runtime"
	"github.com/alibaba/opensandbox/execd/pkg/web/model"
)

const (
	defaultManagedRetentionBytes int64 = 1 << 20
	defaultManagedGrace                = 3 * time.Second
	maxManagedGraceMS            int64 = (1<<63 - 1) / int64(time.Millisecond)
)

// ManagedProcessController handles managed-process control requests.
type ManagedProcessController struct {
	*basicController
	manager *runtime.ManagedProcessManager
}

// NewManagedProcessController creates a controller using the execd-owned manager.
func NewManagedProcessController(ctx *gin.Context, manager *runtime.ManagedProcessManager) *ManagedProcessController {
	return &ManagedProcessController{basicController: newBasicController(ctx), manager: manager}
}

// ResolveExecutable resolves one executable with managed-process environment rules.
func (c *ManagedProcessController) ResolveExecutable() {
	var request model.ResolveManagedExecutableRequest
	if err := c.bindJSON(&request); err != nil {
		c.RespondError(http.StatusBadRequest, model.ErrorCodeInvalidRequest, fmt.Sprintf("error parsing request: %v", err))
		return
	}
	if request.Executable == "" {
		c.RespondError(http.StatusBadRequest, model.ErrorCodeInvalidRequest, "executable is required")
		return
	}
	path, err := c.manager.ResolveExecutable(runtime.ManagedProcessResolveRequest{
		Executable: request.Executable,
		Env:        map[string]*string(request.Env),
	})
	if err != nil {
		c.respondProcessError(err, http.StatusBadRequest)
		return
	}
	c.RespondSuccess(model.ResolveManagedExecutableResponse{Path: path})
}

// Create idempotently starts one exact-argv process.
func (c *ManagedProcessController) Create() {
	var request model.CreateManagedProcessRequest
	if err := c.bindJSON(&request); err != nil {
		c.RespondError(http.StatusBadRequest, model.ErrorCodeInvalidRequest, fmt.Sprintf("error parsing request: %v", err))
		return
	}
	if message := validateManagedProcessRequest(request); message != "" {
		c.RespondError(http.StatusBadRequest, model.ErrorCodeInvalidRequest, message)
		return
	}

	stdoutRetention := defaultManagedRetentionBytes
	if request.StdoutRetentionBytes != nil {
		stdoutRetention = *request.StdoutRetentionBytes
	}
	stderrRetention := defaultManagedRetentionBytes
	if request.StderrRetentionBytes != nil {
		stderrRetention = *request.StderrRetentionBytes
	}
	grace := defaultManagedGrace
	if requestedGrace, _ := optionalGrace(request.GraceMS); requestedGrace != nil {
		grace = *requestedGrace
	}
	process, created, err := c.manager.Create(runtime.ManagedProcessRequest{
		OperationID:          request.OperationID,
		Argv:                 request.Argv,
		Cwd:                  request.Cwd,
		Env:                  map[string]*string(request.Env),
		StdoutRetentionBytes: stdoutRetention,
		StderrRetentionBytes: stderrRetention,
		Grace:                grace,
	})
	if err != nil {
		c.respondProcessError(err, http.StatusInternalServerError)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.ctx.JSON(status, managedProcessStatus(process.Status()))
}

// Get returns one managed-process status.
func (c *ManagedProcessController) Get() {
	process, ok := c.manager.Get(c.ctx.Param("processId"))
	if !ok {
		c.respondProcessError(runtime.ErrManagedProcessNotFound, http.StatusNotFound)
		return
	}
	c.RespondSuccess(managedProcessStatus(process.Status()))
}

// Terminate starts or joins TERM-to-KILL termination.
func (c *ManagedProcessController) Terminate() {
	var request model.TerminateManagedRequest
	if err := c.bindJSON(&request); err != nil && !errors.Is(err, io.EOF) {
		c.RespondError(http.StatusBadRequest, model.ErrorCodeInvalidRequest, fmt.Sprintf("error parsing request: %v", err))
		return
	}
	grace, message := optionalGrace(request.GraceMS)
	if message != "" {
		c.RespondError(http.StatusBadRequest, model.ErrorCodeInvalidRequest, message)
		return
	}
	status, err := c.manager.Terminate(c.ctx.Request.Context(), c.ctx.Param("processId"), grace)
	if err != nil {
		c.respondProcessError(err, http.StatusInternalServerError)
		return
	}
	c.RespondSuccess(managedProcessStatus(status))
}

// Delete removes a quiescent process and its retained output.
func (c *ManagedProcessController) Delete() {
	if err := c.manager.Delete(c.ctx.Param("processId")); err != nil {
		c.respondProcessError(err, http.StatusInternalServerError)
		return
	}
	c.ctx.Status(http.StatusNoContent)
}

func (c *ManagedProcessController) respondProcessError(err error, fallback int) {
	status := fallback
	code := model.ErrorCodeRuntimeError
	switch {
	case errors.Is(err, runtime.ErrManagedProcessNotFound):
		status = http.StatusNotFound
	case errors.Is(err, runtime.ErrManagedProcessInvalidRequest):
		status = http.StatusBadRequest
	case errors.Is(err, runtime.ErrManagedProcessOperationConflict), errors.Is(err, runtime.ErrManagedProcessOperationDeleted), errors.Is(err, runtime.ErrManagedProcessNotQuiescent):
		status = http.StatusConflict
	case errors.Is(err, runtime.ErrManagedProcessManagerClosed):
		status = http.StatusServiceUnavailable
		code = model.ErrorCodeServiceUnavailable
	case errors.Is(err, runtime.ErrManagedProcessUnsupported):
		status = http.StatusNotImplemented
		code = model.ErrorCodeNotSupported
	}
	if status == http.StatusBadRequest {
		code = model.ErrorCodeInvalidRequest
	}
	c.RespondError(status, code, err.Error())
}

// ManagedTerminalController handles managed-terminal control requests.
type ManagedTerminalController struct {
	*basicController
	manager *runtime.ManagedTerminalManager
}

// NewManagedTerminalController creates a controller using the execd-owned manager.
func NewManagedTerminalController(ctx *gin.Context, manager *runtime.ManagedTerminalManager) *ManagedTerminalController {
	return &ManagedTerminalController{basicController: newBasicController(ctx), manager: manager}
}

// Create idempotently starts one exact-argv terminal.
func (c *ManagedTerminalController) Create() {
	var request model.CreateManagedTerminalRequest
	if err := c.bindJSON(&request); err != nil {
		c.RespondError(http.StatusBadRequest, model.ErrorCodeInvalidRequest, fmt.Sprintf("error parsing request: %v", err))
		return
	}
	if message := validateManagedTerminalRequest(request); message != "" {
		c.RespondError(http.StatusBadRequest, model.ErrorCodeInvalidRequest, message)
		return
	}
	grace := defaultManagedGrace
	if requestedGrace, _ := optionalGrace(request.GraceMS); requestedGrace != nil {
		grace = *requestedGrace
	}
	terminal, created, err := c.manager.Create(runtime.ManagedTerminalRequest{
		OperationID: request.OperationID,
		Argv:        request.Argv,
		Cwd:         request.Cwd,
		Env:         map[string]*string(request.Env),
		Rows:        request.Rows,
		Cols:        request.Cols,
		Grace:       grace,
	})
	if err != nil {
		c.respondTerminalError(err, http.StatusInternalServerError)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.ctx.JSON(status, managedTerminalStatus(terminal.Status()))
}

// Get returns one managed-terminal status.
func (c *ManagedTerminalController) Get() {
	terminal, ok := c.manager.Get(c.ctx.Param("terminalId"))
	if !ok {
		c.respondTerminalError(runtime.ErrManagedTerminalNotFound, http.StatusNotFound)
		return
	}
	c.RespondSuccess(managedTerminalStatus(terminal.Status()))
}

// Foreground reports the active foreground process group.
func (c *ManagedTerminalController) Foreground() {
	terminal, ok := c.manager.Get(c.ctx.Param("terminalId"))
	if !ok {
		c.respondTerminalError(runtime.ErrManagedTerminalNotFound, http.StatusNotFound)
		return
	}
	foreground, err := terminal.Foreground()
	if err != nil {
		c.respondTerminalError(err, http.StatusInternalServerError)
		return
	}
	c.RespondSuccess(model.ManagedTerminalForeground{
		ProcessGroup: foreground.ProcessGroup,
		InputWaiting: foreground.InputWaiting,
	})
}

// SignalForeground sends one supported signal and reports the group that received it.
func (c *ManagedTerminalController) SignalForeground() {
	var request model.SignalManagedTerminalRequest
	if err := c.bindJSON(&request); err != nil {
		c.RespondError(http.StatusBadRequest, model.ErrorCodeInvalidRequest, fmt.Sprintf("error parsing request: %v", err))
		return
	}
	if !managedTerminalSignalSupported(request.Signal) {
		c.RespondError(http.StatusBadRequest, model.ErrorCodeInvalidRequest, "unsupported terminal signal")
		return
	}
	terminal, ok := c.manager.Get(c.ctx.Param("terminalId"))
	if !ok {
		c.respondTerminalError(runtime.ErrManagedTerminalNotFound, http.StatusNotFound)
		return
	}
	group, err := terminal.SignalForeground(runtime.ManagedTerminalSignal(request.Signal))
	if err != nil {
		c.respondTerminalError(err, http.StatusInternalServerError)
		return
	}
	c.RespondSuccess(model.SignalManagedTerminalResponse{ProcessGroup: group})
}

// Terminate starts or joins complete terminal-session termination.
func (c *ManagedTerminalController) Terminate() {
	var request model.TerminateManagedRequest
	if err := c.bindJSON(&request); err != nil && !errors.Is(err, io.EOF) {
		c.RespondError(http.StatusBadRequest, model.ErrorCodeInvalidRequest, fmt.Sprintf("error parsing request: %v", err))
		return
	}
	grace, message := optionalGrace(request.GraceMS)
	if message != "" {
		c.RespondError(http.StatusBadRequest, model.ErrorCodeInvalidRequest, message)
		return
	}
	status, err := c.manager.Terminate(c.ctx.Request.Context(), c.ctx.Param("terminalId"), grace)
	if err != nil {
		c.respondTerminalError(err, http.StatusInternalServerError)
		return
	}
	c.RespondSuccess(managedTerminalStatus(status))
}

// Delete removes a quiescent terminal and retained output.
func (c *ManagedTerminalController) Delete() {
	if err := c.manager.Delete(c.ctx.Param("terminalId")); err != nil {
		c.respondTerminalError(err, http.StatusInternalServerError)
		return
	}
	c.ctx.Status(http.StatusNoContent)
}

func (c *ManagedTerminalController) respondTerminalError(err error, fallback int) {
	status := fallback
	code := model.ErrorCodeRuntimeError
	switch {
	case errors.Is(err, runtime.ErrManagedTerminalNotFound):
		status = http.StatusNotFound
	case errors.Is(err, runtime.ErrManagedTerminalInvalidRequest):
		status = http.StatusBadRequest
	case errors.Is(err, runtime.ErrManagedTerminalOperationConflict), errors.Is(err, runtime.ErrManagedTerminalOperationDeleted), errors.Is(err, runtime.ErrManagedTerminalNotQuiescent), errors.Is(err, runtime.ErrManagedTerminalClosing), errors.Is(err, runtime.ErrManagedTerminalInactive):
		status = http.StatusConflict
	case errors.Is(err, runtime.ErrManagedTerminalManagerClosed):
		status = http.StatusServiceUnavailable
		code = model.ErrorCodeServiceUnavailable
	case errors.Is(err, runtime.ErrManagedTerminalUnsupported):
		status = http.StatusNotImplemented
		code = model.ErrorCodeNotSupported
	case errors.Is(err, runtime.ErrManagedTerminalSignal):
		status = http.StatusBadRequest
	}
	if status == http.StatusBadRequest {
		code = model.ErrorCodeInvalidRequest
	}
	c.RespondError(status, code, err.Error())
}

func validateManagedProcessRequest(request model.CreateManagedProcessRequest) string {
	if request.OperationID == "" {
		return "operationId is required"
	}
	if len(request.Argv) == 0 || request.Argv[0] == "" {
		return "argv must contain an executable"
	}
	if !filepath.IsAbs(request.Cwd) {
		return "cwd must be absolute"
	}
	if request.Stdin != "pipe" {
		return "stdin must be pipe"
	}
	if request.StdoutRetentionBytes != nil && *request.StdoutRetentionBytes < 0 ||
		request.StderrRetentionBytes != nil && *request.StderrRetentionBytes < 0 {
		return "retention bytes must not be negative"
	}
	if _, message := optionalGrace(request.GraceMS); message != "" {
		return message
	}
	for _, arg := range request.Argv {
		if strings.ContainsRune(arg, 0) {
			return "argv must not contain NUL"
		}
	}
	return ""
}

func validateManagedTerminalRequest(request model.CreateManagedTerminalRequest) string {
	processRequest := model.CreateManagedProcessRequest{
		OperationID: request.OperationID,
		Argv:        request.Argv,
		Cwd:         request.Cwd,
		Stdin:       "pipe",
		GraceMS:     request.GraceMS,
	}
	if message := validateManagedProcessRequest(processRequest); message != "" {
		return message
	}
	if request.Rows == 0 || request.Cols == 0 {
		return "rows and cols must be positive"
	}
	return ""
}

func optionalGrace(milliseconds *int64) (*time.Duration, string) {
	if milliseconds == nil {
		return nil, ""
	}
	if *milliseconds < 0 {
		return nil, "graceMs must not be negative"
	}
	if *milliseconds > maxManagedGraceMS {
		return nil, "graceMs is too large"
	}
	grace := time.Duration(*milliseconds) * time.Millisecond
	return &grace, ""
}

func managedTerminalSignalSupported(signal string) bool {
	switch runtime.ManagedTerminalSignal(signal) {
	case runtime.ManagedTerminalSignalInterrupt,
		runtime.ManagedTerminalSignalTerminate,
		runtime.ManagedTerminalSignalKill,
		runtime.ManagedTerminalSignalStop,
		runtime.ManagedTerminalSignalHangup:
		return true
	default:
		return false
	}
}

func managedProcessStatus(status runtime.ManagedProcessStatus) model.ManagedProcessStatus {
	pid := status.PID
	return model.ManagedProcessStatus{
		ProcessID:          status.ProcessID,
		PID:                &pid,
		State:              string(status.State),
		ExitCode:           status.ExitCode,
		Signal:             status.Signal,
		TopLevelExited:     status.TopLevelExited,
		TreeEmpty:          status.TreeEmpty,
		StdinSequence:      status.StdinSequence,
		StdoutOffset:       status.StdoutOffset,
		StderrOffset:       status.StderrOffset,
		StdoutRetainedFrom: status.StdoutRetainedFrom,
		StderrRetainedFrom: status.StderrRetainedFrom,
		StdoutSpillPath:    status.StdoutSpillPath,
		StderrSpillPath:    status.StderrSpillPath,
	}
}

func managedTerminalStatus(status runtime.ManagedTerminalStatus) model.ManagedTerminalStatus {
	pid := status.PID
	return model.ManagedTerminalStatus{
		TerminalID:         status.TerminalID,
		PID:                &pid,
		State:              string(status.State),
		ExitCode:           status.ExitCode,
		Signal:             status.Signal,
		TopLevelExited:     status.TopLevelExited,
		TreeEmpty:          status.TreeEmpty,
		OutputOffset:       status.OutputOffset,
		OutputRetainedFrom: status.OutputRetainedFrom,
		OutputEOF:          status.OutputEOF,
	}
}
