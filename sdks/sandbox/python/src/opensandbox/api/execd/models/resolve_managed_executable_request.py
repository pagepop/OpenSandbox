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
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_environment import ManagedEnvironment


T = TypeVar("T", bound="ResolveManagedExecutableRequest")


@_attrs_define
class ResolveManagedExecutableRequest:
    """
    Attributes:
        executable (str):
        env (ManagedEnvironment | Unset): Environment patch applied over execd's scrubbed base; null removes a name.
    """

    executable: str
    env: ManagedEnvironment | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        executable = self.executable

        env: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env, Unset):
            env = self.env.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "executable": executable,
            }
        )
        if env is not UNSET:
            field_dict["env"] = env

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_environment import ManagedEnvironment

        d = dict(src_dict)
        executable = d.pop("executable")

        _env = d.pop("env", UNSET)
        env: ManagedEnvironment | Unset
        if isinstance(_env, Unset):
            env = UNSET
        else:
            env = ManagedEnvironment.from_dict(_env)

        resolve_managed_executable_request = cls(
            executable=executable,
            env=env,
        )

        resolve_managed_executable_request.additional_properties = d
        return resolve_managed_executable_request

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
