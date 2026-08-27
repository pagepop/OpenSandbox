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
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.error_response import ErrorResponse
from ...models.managed_terminal_foreground import ManagedTerminalForeground
from ...types import Response


def _get_kwargs(
    terminal_id: str,
) -> dict[str, Any]:
    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/terminals/{terminal_id}/foreground".format(
            terminal_id=quote(str(terminal_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ErrorResponse | ManagedTerminalForeground | None:
    if response.status_code == 200:
        response_200 = ManagedTerminalForeground.from_dict(response.json())

        return response_200

    if response.status_code == 404:
        response_404 = ErrorResponse.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = ErrorResponse.from_dict(response.json())

        return response_409

    if response.status_code == 500:
        response_500 = ErrorResponse.from_dict(response.json())

        return response_500

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ErrorResponse | ManagedTerminalForeground]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    terminal_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ErrorResponse | ManagedTerminalForeground]:
    """Inspect the terminal foreground process group

    Args:
        terminal_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorResponse | ManagedTerminalForeground]
    """

    kwargs = _get_kwargs(
        terminal_id=terminal_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    terminal_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> ErrorResponse | ManagedTerminalForeground | None:
    """Inspect the terminal foreground process group

    Args:
        terminal_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorResponse | ManagedTerminalForeground
    """

    return sync_detailed(
        terminal_id=terminal_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    terminal_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ErrorResponse | ManagedTerminalForeground]:
    """Inspect the terminal foreground process group

    Args:
        terminal_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorResponse | ManagedTerminalForeground]
    """

    kwargs = _get_kwargs(
        terminal_id=terminal_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    terminal_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> ErrorResponse | ManagedTerminalForeground | None:
    """Inspect the terminal foreground process group

    Args:
        terminal_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorResponse | ManagedTerminalForeground
    """

    return (
        await asyncio_detailed(
            terminal_id=terminal_id,
            client=client,
        )
    ).parsed
