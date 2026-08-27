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
from ...models.managed_process_status import ManagedProcessStatus
from ...models.terminate_managed_request import TerminateManagedRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    process_id: str,
    *,
    body: TerminateManagedRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/processes/{process_id}/terminate".format(
            process_id=quote(str(process_id), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ErrorResponse | ManagedProcessStatus | None:
    if response.status_code == 200:
        response_200 = ManagedProcessStatus.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = ErrorResponse.from_dict(response.json())

        return response_400

    if response.status_code == 404:
        response_404 = ErrorResponse.from_dict(response.json())

        return response_404

    if response.status_code == 500:
        response_500 = ErrorResponse.from_dict(response.json())

        return response_500

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ErrorResponse | ManagedProcessStatus]:
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
    body: TerminateManagedRequest | Unset = UNSET,
) -> Response[ErrorResponse | ManagedProcessStatus]:
    """Terminate a managed process group

     Starts or joins idempotent TERM-to-KILL termination and waits for group quiescence.

    Args:
        process_id (str):
        body (TerminateManagedRequest | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorResponse | ManagedProcessStatus]
    """

    kwargs = _get_kwargs(
        process_id=process_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    process_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: TerminateManagedRequest | Unset = UNSET,
) -> ErrorResponse | ManagedProcessStatus | None:
    """Terminate a managed process group

     Starts or joins idempotent TERM-to-KILL termination and waits for group quiescence.

    Args:
        process_id (str):
        body (TerminateManagedRequest | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorResponse | ManagedProcessStatus
    """

    return sync_detailed(
        process_id=process_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    process_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: TerminateManagedRequest | Unset = UNSET,
) -> Response[ErrorResponse | ManagedProcessStatus]:
    """Terminate a managed process group

     Starts or joins idempotent TERM-to-KILL termination and waits for group quiescence.

    Args:
        process_id (str):
        body (TerminateManagedRequest | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorResponse | ManagedProcessStatus]
    """

    kwargs = _get_kwargs(
        process_id=process_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    process_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: TerminateManagedRequest | Unset = UNSET,
) -> ErrorResponse | ManagedProcessStatus | None:
    """Terminate a managed process group

     Starts or joins idempotent TERM-to-KILL termination and waits for group quiescence.

    Args:
        process_id (str):
        body (TerminateManagedRequest | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorResponse | ManagedProcessStatus
    """

    return (
        await asyncio_detailed(
            process_id=process_id,
            client=client,
            body=body,
        )
    ).parsed
