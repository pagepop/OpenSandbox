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

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
)

func migrationOldConnectionConfig(t *testing.T) opensandbox.ConnectionConfig {
	t.Helper()
	domain := os.Getenv("OPENSANDBOX_MIGRATION_OLD_DOMAIN")
	if domain == "" {
		t.Skip("set OPENSANDBOX_MIGRATION_OLD_DOMAIN to run old-vs-new migration e2e")
	}
	protocol := os.Getenv("OPENSANDBOX_MIGRATION_OLD_PROTOCOL")
	if protocol == "" {
		protocol = "http"
	}
	apiKey := os.Getenv("OPENSANDBOX_MIGRATION_OLD_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("OPENSANDBOX_TEST_API_KEY")
	}
	if apiKey == "" {
		apiKey = "e2e-test"
	}
	authHeader := os.Getenv("OPENSANDBOX_MIGRATION_OLD_AUTH_HEADER")
	if authHeader == "" {
		authHeader = "OPEN-SANDBOX-API-KEY"
	}
	return opensandbox.ConnectionConfig{
		Domain:     domain,
		Protocol:   protocol,
		APIKey:     apiKey,
		AuthHeader: authHeader,
	}
}

func migrationSharedPVCName() string {
	if v := os.Getenv("OPENSANDBOX_MIGRATION_PVC_NAME"); v != "" {
		return v
	}
	return getPVCName()
}

func migrationSubPaths() (string, string) {
	left := os.Getenv("OPENSANDBOX_MIGRATION_PVC_SUBPATH_LEFT")
	if left == "" {
		left = "skill-hub/publish"
	}
	right := os.Getenv("OPENSANDBOX_MIGRATION_PVC_SUBPATH_RIGHT")
	if right == "" {
		right = "skill-hub/draft"
	}
	return left, right
}

func rawLifecycleURL(config opensandbox.ConnectionConfig, path string) string {
	protocol := config.Protocol
	if protocol == "" {
		protocol = "http"
	}
	return fmt.Sprintf("%s://%s/v1%s", protocol, strings.TrimRight(config.Domain, "/"), path)
}

func createLegacyRawMountSandbox(
	t *testing.T,
	ctx context.Context,
	config opensandbox.ConnectionConfig,
	pvcName string,
	leftSubPath string,
	rightSubPath string,
) *opensandbox.Sandbox {
	t.Helper()
	payload := map[string]any{
		"image":          map[string]any{"uri": getSandboxImage()},
		"entrypoint":     []string{"tail", "-f", "/dev/null"},
		"resourceLimits": map[string]string{"cpu": "500m", "memory": "512Mi"},
		"timeout":        120,
		"env":            map[string]string{"EXECD_API_GRACE_SHUTDOWN": "3s"},
		"metadata":       map[string]string{"migrationCase": "legacy-raw-mounts"},
		"volumes": []map[string]any{
			{
				"name": "pagepop-shared-pvc",
				"persistentVolumeClaim": map[string]any{
					"claimName": pvcName,
				},
			},
		},
		"mounts": []map[string]any{
			{
				"name":      "pagepop-shared-pvc",
				"mountPath": "/opt/pagepop/skills",
				"readOnly":  true,
				"subPath":   leftSubPath,
			},
			{
				"name":      "pagepop-shared-pvc",
				"mountPath": "/opt/pagepop/draft",
				"readOnly":  true,
				"subPath":   rightSubPath,
			},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		rawLifecycleURL(config, "/sandboxes"),
		bytes.NewReader(body),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if config.APIKey != "" {
		req.Header.Set(config.GetAuthHeader(), config.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var created opensandbox.SandboxInfo
	if resp.StatusCode != http.StatusAccepted {
		var errBody map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		require.Equalf(t, http.StatusAccepted, resp.StatusCode, "legacy raw create response: %#v", errBody)
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.NotEmpty(t, created.ID)

	sb, err := opensandbox.ConnectSandbox(ctx, config, created.ID, opensandbox.ReadyOptions{
		Timeout: 60 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sb.Kill(context.Background()) })
	return sb
}

func createOfficialSharedPVCSandbox(
	t *testing.T,
	ctx context.Context,
	config opensandbox.ConnectionConfig,
	pvcName string,
	leftSubPath string,
	rightSubPath string,
) *opensandbox.Sandbox {
	t.Helper()
	sb, err := opensandbox.CreateSandbox(ctx, config, opensandbox.SandboxCreateOptions{
		Image:        getSandboxImage(),
		Env:          map[string]string{"EXECD_API_GRACE_SHUTDOWN": "3s"},
		Entrypoint:   []string{"tail", "-f", "/dev/null"},
		ReadyTimeout: 60 * time.Second,
		Volumes: []opensandbox.Volume{
			{
				Name:      "skills",
				PVC:       &opensandbox.PVC{ClaimName: pvcName},
				MountPath: "/opt/pagepop/skills",
				ReadOnly:  true,
				SubPath:   leftSubPath,
			},
			{
				Name:      "draft",
				PVC:       &opensandbox.PVC{ClaimName: pvcName},
				MountPath: "/opt/pagepop/draft",
				ReadOnly:  true,
				SubPath:   rightSubPath,
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sb.Kill(context.Background()) })
	return sb
}

func verifyPagePopSharedPVCMounts(t *testing.T, ctx context.Context, sb *opensandbox.Sandbox) {
	t.Helper()
	for _, mountPath := range []string{"/opt/pagepop/skills", "/opt/pagepop/draft"} {
		exec, err := sb.RunCommand(ctx, fmt.Sprintf("test -d %s && echo mounted:%s", mountPath, mountPath), nil)
		require.NoError(t, err)
		require.Contains(t, exec.Text(), "mounted:"+mountPath)

		exec, err = sb.RunCommand(
			ctx,
			fmt.Sprintf("touch %s/opensandbox-write-probe 2>/tmp/write.err; echo EXIT_CODE=$?; cat /tmp/write.err", mountPath),
			nil,
		)
		require.NoError(t, err)
		require.NotContains(t, exec.Text(), "EXIT_CODE=0", "read-only mount unexpectedly allowed writes")
	}
}

func TestVolumeMigration_LegacyRawMountsAndOfficialVolumesBehaveTheSame(t *testing.T) {
	oldConfig := migrationOldConnectionConfig(t)
	newConfig := connectionConfigForStreaming(t)
	pvcName := migrationSharedPVCName()
	leftSubPath, rightSubPath := migrationSubPaths()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	oldSandbox := createLegacyRawMountSandbox(t, ctx, oldConfig, pvcName, leftSubPath, rightSubPath)
	newSandbox := createOfficialSharedPVCSandbox(t, ctx, newConfig, pvcName, leftSubPath, rightSubPath)

	verifyPagePopSharedPVCMounts(t, ctx, oldSandbox)
	verifyPagePopSharedPVCMounts(t, ctx, newSandbox)
}
