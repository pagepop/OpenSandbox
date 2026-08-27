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

from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_environment import ManagedEnvironment


T = TypeVar("T", bound="CreateManagedTerminalRequest")


@_attrs_define
class CreateManagedTerminalRequest:
    """
    Attributes:
        operation_id (str):
        argv (list[str]): Exact argv vector passed without shell interpretation
        cwd (str): Absolute working directory
        rows (int):
        cols (int):
        env (ManagedEnvironment | Unset): Environment patch applied over execd's scrubbed base; null removes a name.
        grace_ms (int | Unset): Create-time TERM-to-KILL grace; omission uses 3000.
    """

    operation_id: str
    argv: list[str]
    cwd: str
    rows: int
    cols: int
    env: ManagedEnvironment | Unset = UNSET
    grace_ms: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        operation_id = self.operation_id

        argv = self.argv

        cwd = self.cwd

        rows = self.rows

        cols = self.cols

        env: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env, Unset):
            env = self.env.to_dict()

        grace_ms = self.grace_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "operationId": operation_id,
                "argv": argv,
                "cwd": cwd,
                "rows": rows,
                "cols": cols,
            }
        )
        if env is not UNSET:
            field_dict["env"] = env
        if grace_ms is not UNSET:
            field_dict["graceMs"] = grace_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_environment import ManagedEnvironment

        d = dict(src_dict)
        operation_id = d.pop("operationId")

        argv = cast(list[str], d.pop("argv"))

        cwd = d.pop("cwd")

        rows = d.pop("rows")

        cols = d.pop("cols")

        _env = d.pop("env", UNSET)
        env: ManagedEnvironment | Unset
        if isinstance(_env, Unset):
            env = UNSET
        else:
            env = ManagedEnvironment.from_dict(_env)

        grace_ms = d.pop("graceMs", UNSET)

        create_managed_terminal_request = cls(
            operation_id=operation_id,
            argv=argv,
            cwd=cwd,
            rows=rows,
            cols=cols,
            env=env,
            grace_ms=grace_ms,
        )

        create_managed_terminal_request.additional_properties = d
        return create_managed_terminal_request

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
