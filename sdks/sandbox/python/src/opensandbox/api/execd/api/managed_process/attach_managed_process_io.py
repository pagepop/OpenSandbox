#
# Copyright 2026 Alibaba Group Holding Ltd.
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
#

from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.error_response import ErrorResponse
from ...types import UNSET, Response


def _get_kwargs(
    process_id: str,
    *,
    stdin_sequence: int,
    stdout_offset: int,
    stderr_offset: int,
) -> dict[str, Any]:
    params: dict[str, Any] = {}

    params["stdinSequence"] = stdin_sequence

    params["stdoutOffset"] = stdout_offset

    params["stderrOffset"] = stderr_offset

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/processes/{process_id}/io".format(
            process_id=quote(str(process_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | ErrorResponse | None:
    if response.status_code == 101:
        response_101 = cast(Any, None)
        return response_101

    if response.status_code == 400:
        response_400 = ErrorResponse.from_dict(response.json())

        return response_400

    if response.status_code == 404:
        response_404 = ErrorResponse.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | ErrorResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    process_id: str,
    *,
    client: AuthenticatedClient | Client,
    stdin_sequence: int,
    stdout_offset: int,
    stderr_offset: int,
) -> Response[Any | ErrorResponse]:
    """Attach managed-process standard I/O

     Upgrades to the OSEP-0015 WebSocket protocol. Client binary frames are
    `[0x00][uint64 sequence, big-endian][stdin bytes]`; server binary frames are
    `[0x01|0x02][uint64 offset, big-endian][stdout|stderr bytes]`. Text control
    frames publish connected positions, stdin acknowledgements and EOF, output
    gaps and EOF, the direct-process outcome, and protocol errors. A newer
    attachment replaces the prior attachment, which closes with code 4001.

    Args:
        process_id (str):
        stdin_sequence (int):
        stdout_offset (int):
        stderr_offset (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ErrorResponse]
    """

    kwargs = _get_kwargs(
        process_id=process_id,
        stdin_sequence=stdin_sequence,
        stdout_offset=stdout_offset,
        stderr_offset=stderr_offset,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    process_id: str,
    *,
    client: AuthenticatedClient | Client,
    stdin_sequence: int,
    stdout_offset: int,
    stderr_offset: int,
) -> Any | ErrorResponse | None:
    """Attach managed-process standard I/O

     Upgrades to the OSEP-0015 WebSocket protocol. Client binary frames are
    `[0x00][uint64 sequence, big-endian][stdin bytes]`; server binary frames are
    `[0x01|0x02][uint64 offset, big-endian][stdout|stderr bytes]`. Text control
    frames publish connected positions, stdin acknowledgements and EOF, output
    gaps and EOF, the direct-process outcome, and protocol errors. A newer
    attachment replaces the prior attachment, which closes with code 4001.

    Args:
        process_id (str):
        stdin_sequence (int):
        stdout_offset (int):
        stderr_offset (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ErrorResponse
    """

    return sync_detailed(
        process_id=process_id,
        client=client,
        stdin_sequence=stdin_sequence,
        stdout_offset=stdout_offset,
        stderr_offset=stderr_offset,
    ).parsed


async def asyncio_detailed(
    process_id: str,
    *,
    client: AuthenticatedClient | Client,
    stdin_sequence: int,
    stdout_offset: int,
    stderr_offset: int,
) -> Response[Any | ErrorResponse]:
    """Attach managed-process standard I/O

     Upgrades to the OSEP-0015 WebSocket protocol. Client binary frames are
    `[0x00][uint64 sequence, big-endian][stdin bytes]`; server binary frames are
    `[0x01|0x02][uint64 offset, big-endian][stdout|stderr bytes]`. Text control
    frames publish connected positions, stdin acknowledgements and EOF, output
    gaps and EOF, the direct-process outcome, and protocol errors. A newer
    attachment replaces the prior attachment, which closes with code 4001.

    Args:
        process_id (str):
        stdin_sequence (int):
        stdout_offset (int):
        stderr_offset (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ErrorResponse]
    """

    kwargs = _get_kwargs(
        process_id=process_id,
        stdin_sequence=stdin_sequence,
        stdout_offset=stdout_offset,
        stderr_offset=stderr_offset,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    process_id: str,
    *,
    client: AuthenticatedClient | Client,
    stdin_sequence: int,
    stdout_offset: int,
    stderr_offset: int,
) -> Any | ErrorResponse | None:
    """Attach managed-process standard I/O

     Upgrades to the OSEP-0015 WebSocket protocol. Client binary frames are
    `[0x00][uint64 sequence, big-endian][stdin bytes]`; server binary frames are
    `[0x01|0x02][uint64 offset, big-endian][stdout|stderr bytes]`. Text control
    frames publish connected positions, stdin acknowledgements and EOF, output
    gaps and EOF, the direct-process outcome, and protocol errors. A newer
    attachment replaces the prior attachment, which closes with code 4001.

    Args:
        process_id (str):
        stdin_sequence (int):
        stdout_offset (int):
        stderr_offset (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ErrorResponse
    """

    return (
        await asyncio_detailed(
            process_id=process_id,
            client=client,
            stdin_sequence=stdin_sequence,
            stdout_offset=stdout_offset,
            stderr_offset=stderr_offset,
        )
    ).parsed
