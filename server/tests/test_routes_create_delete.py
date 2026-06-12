# Copyright 2025 Alibaba Group Holding Ltd.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from datetime import datetime, timedelta, timezone

from fastapi.testclient import TestClient

from opensandbox_server.api import lifecycle
from opensandbox_server.api.schema import CreateSandboxResponse, SandboxStatus


def test_create_sandbox_returns_202_and_service_payload(
    client: TestClient,
    auth_headers: dict,
    sample_sandbox_request: dict,
    monkeypatch,
) -> None:
    now = datetime.now(timezone.utc)
    calls: list[object] = []

    class StubService:
        @staticmethod
        async def create_sandbox(request) -> CreateSandboxResponse:
            calls.append(request)
            return CreateSandboxResponse(
                id="sbx-001",
                status=SandboxStatus(state="Pending"),
                metadata={"project": "test-project"},
                expiresAt=now + timedelta(hours=1),
                createdAt=now,
                entrypoint=["python", "-c", "print('Hello from sandbox')"],
            )

    monkeypatch.setattr(lifecycle, "sandbox_service", StubService())

    response = client.post(
        "/v1/sandboxes",
        headers=auth_headers,
        json=sample_sandbox_request,
    )

    assert response.status_code == 202
    payload = response.json()
    assert payload["id"] == "sbx-001"
    assert payload["status"]["state"] == "Pending"
    assert payload["metadata"]["project"] == "test-project"
    assert payload["entrypoint"] == ["python", "-c", "print('Hello from sandbox')"]
    assert len(calls) == 1
    assert calls[0].image.uri == "python:3.11"


def test_create_sandbox_manual_cleanup_omits_none_fields(
    client: TestClient,
    auth_headers: dict,
    sample_sandbox_request: dict,
    monkeypatch,
) -> None:
    now = datetime.now(timezone.utc)

    class StubService:
        @staticmethod
        async def create_sandbox(request) -> CreateSandboxResponse:
            return CreateSandboxResponse(
                id="sbx-manual",
                status=SandboxStatus(state="Pending"),
                metadata=None,
                expiresAt=None,
                createdAt=now,
                entrypoint=["python", "-c", "print('Hello from sandbox')"],
            )

    monkeypatch.setattr(lifecycle, "sandbox_service", StubService())
    sample_sandbox_request.pop("timeout", None)

    response = client.post(
        "/v1/sandboxes",
        headers=auth_headers,
        json=sample_sandbox_request,
    )

    assert response.status_code == 202
    payload = response.json()
    assert "expiresAt" not in payload
    assert "metadata" not in payload
    assert "reason" not in payload["status"]
    assert "message" not in payload["status"]
    assert "lastTransitionAt" not in payload["status"]


def test_create_sandbox_rejects_invalid_request(
    client: TestClient,
    auth_headers: dict,
) -> None:
    response = client.post(
        "/v1/sandboxes",
        headers=auth_headers,
        json={"timeout": 10},
    )

    assert response.status_code == 422


def test_create_sandbox_accepts_snapshot_id_without_entrypoint(
    client: TestClient,
    auth_headers: dict,
    monkeypatch,
) -> None:
    now = datetime.now(timezone.utc)
    calls: list[object] = []

    class StubService:
        @staticmethod
        async def create_sandbox(request) -> CreateSandboxResponse:
            calls.append(request)
            return CreateSandboxResponse(
                id="sbx-from-snapshot",
                status=SandboxStatus(state="Pending"),
                metadata=None,
                expiresAt=now + timedelta(hours=1),
                createdAt=now,
                entrypoint=None,
            )

    monkeypatch.setattr(lifecycle, "sandbox_service", StubService())

    response = client.post(
        "/v1/sandboxes",
        headers=auth_headers,
        json={
            "snapshotId": "snap-001",
            "resourceLimits": {"cpu": "500m", "memory": "512Mi"},
        },
    )

    assert response.status_code == 202
    assert calls[0].snapshot_id == "snap-001"
    assert calls[0].entrypoint is None


def test_create_sandbox_accepts_snapshot_id_with_entrypoint(
    client: TestClient,
    auth_headers: dict,
    monkeypatch,
) -> None:
    now = datetime.now(timezone.utc)
    calls: list[object] = []

    class StubService:
        @staticmethod
        async def create_sandbox(request) -> CreateSandboxResponse:
            calls.append(request)
            return CreateSandboxResponse(
                id="sbx-from-snapshot",
                status=SandboxStatus(state="Pending"),
                metadata=None,
                expiresAt=now + timedelta(hours=1),
                createdAt=now,
                entrypoint=["python", "app.py"],
            )

    monkeypatch.setattr(lifecycle, "sandbox_service", StubService())

    response = client.post(
        "/v1/sandboxes",
        headers=auth_headers,
        json={
            "snapshotId": "snap-001",
            "resourceLimits": {"cpu": "500m", "memory": "512Mi"},
            "entrypoint": ["python", "app.py"],
        },
    )

    assert response.status_code == 202
    assert calls[0].snapshot_id == "snap-001"
    assert calls[0].entrypoint == ["python", "app.py"]


def test_create_sandbox_pagepop_shared_pvc_volumes_reach_service(
    client: TestClient,
    auth_headers: dict,
    monkeypatch,
) -> None:
    now = datetime.now(timezone.utc)
    calls: list[object] = []

    class StubService:
        @staticmethod
        async def create_sandbox(request) -> CreateSandboxResponse:
            calls.append(request)
            return CreateSandboxResponse(
                id="sbx-pagepop-volumes",
                status=SandboxStatus(state="Pending"),
                metadata={"case": "pagepop-shared-pvc"},
                expiresAt=now + timedelta(hours=1),
                createdAt=now,
                entrypoint=["tail", "-f", "/dev/null"],
            )

    monkeypatch.setattr(lifecycle, "sandbox_service", StubService())

    response = client.post(
        "/v1/sandboxes",
        headers=auth_headers,
        json={
            "image": {"uri": "python:3.11"},
            "timeout": 3600,
            "resourceLimits": {"cpu": "500m", "memory": "512Mi"},
            "entrypoint": ["tail", "-f", "/dev/null"],
            "volumes": [
                {
                    "name": "skills",
                    "pvc": {"claimName": "oss-pvc-r"},
                    "mountPath": "/opt/pagepop/skills",
                    "readOnly": True,
                    "subPath": "skill-hub/publish",
                },
                {
                    "name": "draft",
                    "pvc": {"claimName": "oss-pvc-r"},
                    "mountPath": "/opt/pagepop/draft",
                    "readOnly": True,
                    "subPath": "skill-hub/draft",
                },
            ],
        },
    )

    assert response.status_code == 202
    assert len(calls) == 1
    request = calls[0]
    assert request.volumes is not None
    assert [volume.name for volume in request.volumes] == ["skills", "draft"]
    assert {volume.pvc.claim_name for volume in request.volumes if volume.pvc} == {
        "oss-pvc-r"
    }
    assert [volume.mount_path for volume in request.volumes] == [
        "/opt/pagepop/skills",
        "/opt/pagepop/draft",
    ]
    assert [volume.sub_path for volume in request.volumes] == [
        "skill-hub/publish",
        "skill-hub/draft",
    ]
    assert all(volume.read_only is True for volume in request.volumes)


def test_create_sandbox_legacy_raw_mounts_do_not_reach_service_as_volumes(
    client: TestClient,
    auth_headers: dict,
    monkeypatch,
) -> None:
    now = datetime.now(timezone.utc)
    calls: list[object] = []

    class StubService:
        @staticmethod
        async def create_sandbox(request) -> CreateSandboxResponse:
            calls.append(request)
            return CreateSandboxResponse(
                id="sbx-legacy-raw-mounts",
                status=SandboxStatus(state="Pending"),
                metadata={"case": "legacy-raw-mounts"},
                expiresAt=now + timedelta(hours=1),
                createdAt=now,
                entrypoint=["tail", "-f", "/dev/null"],
            )

    monkeypatch.setattr(lifecycle, "sandbox_service", StubService())

    response = client.post(
        "/v1/sandboxes",
        headers=auth_headers,
        json={
            "image": {"uri": "python:3.11"},
            "timeout": 3600,
            "resourceLimits": {"cpu": "500m", "memory": "512Mi"},
            "entrypoint": ["tail", "-f", "/dev/null"],
            "mounts": [
                {
                    "name": "legacy-skills",
                    "mountPath": "/opt/pagepop/skills",
                    "readOnly": True,
                    "subPath": "skill-hub/publish",
                }
            ],
        },
    )

    assert response.status_code == 202
    assert len(calls) == 1
    assert calls[0].volumes is None


def test_delete_sandbox_returns_204_and_calls_service(
    client: TestClient,
    auth_headers: dict,
    monkeypatch,
) -> None:
    calls: list[str] = []

    class StubService:
        @staticmethod
        def delete_sandbox(sandbox_id: str) -> None:
            calls.append(sandbox_id)

    monkeypatch.setattr(lifecycle, "sandbox_service", StubService())

    response = client.delete("/v1/sandboxes/sbx-001", headers=auth_headers)

    assert response.status_code == 204
    assert response.text == ""
    assert calls == ["sbx-001"]


def test_delete_sandbox_requires_api_key(client: TestClient) -> None:
    response = client.delete("/v1/sandboxes/sbx-001")

    assert response.status_code == 401
    assert response.json()["code"] == "MISSING_API_KEY"
