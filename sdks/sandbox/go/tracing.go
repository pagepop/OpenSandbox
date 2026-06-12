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

package opensandbox

import (
	"context"
	"sync"
)

// TraceSpan is implemented by host applications that want OpenSandbox SDK
// request metadata to be attached to their own tracing system.
type TraceSpan interface {
	SetTags(context.Context, map[string]any)
	SetOutput(context.Context, any)
	SetError(context.Context, error)
}

// TraceHook wraps one SDK operation in a caller-owned tracing span.
type TraceHook func(ctx context.Context, spanName string, tags map[string]any, fn func(context.Context, TraceSpan) error) error

type noopTraceSpan struct{}

func (noopTraceSpan) SetTags(context.Context, map[string]any) {}

func (noopTraceSpan) SetOutput(context.Context, any) {}

func (noopTraceSpan) SetError(context.Context, error) {}

var traceHookState struct {
	sync.RWMutex
	hook TraceHook
}

// SetTraceHook installs a process-wide hook used by all SDK clients.
func SetTraceHook(hook TraceHook) {
	traceHookState.Lock()
	traceHookState.hook = hook
	traceHookState.Unlock()
}

func getTraceHook() TraceHook {
	traceHookState.RLock()
	hook := traceHookState.hook
	traceHookState.RUnlock()
	return hook
}

func withTraceSpan(
	ctx context.Context,
	spanName string,
	tags map[string]any,
	fn func(context.Context, TraceSpan) error,
) error {
	if hook := getTraceHook(); hook != nil {
		return hook(ctx, spanName, tags, fn)
	}
	return fn(ctx, noopTraceSpan{})
}
