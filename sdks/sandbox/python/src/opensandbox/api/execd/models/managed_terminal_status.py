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
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_terminal_status_state import ManagedTerminalStatusState
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedTerminalStatus")


@_attrs_define
class ManagedTerminalStatus:
    """
    Attributes:
        terminal_id (str):
        state (ManagedTerminalStatusState):
        exit_code (int | None):
        signal (None | str):
        top_level_exited (bool):
        tree_empty (bool):
        output_offset (int):
        output_retained_from (int):
        output_eof (bool):
        pid (int | Unset): Diagnostic operating-system PID, present after publication
    """

    terminal_id: str
    state: ManagedTerminalStatusState
    exit_code: int | None
    signal: None | str
    top_level_exited: bool
    tree_empty: bool
    output_offset: int
    output_retained_from: int
    output_eof: bool
    pid: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        terminal_id = self.terminal_id

        state = self.state.value

        exit_code: int | None
        exit_code = self.exit_code

        signal: None | str
        signal = self.signal

        top_level_exited = self.top_level_exited

        tree_empty = self.tree_empty

        output_offset = self.output_offset

        output_retained_from = self.output_retained_from

        output_eof = self.output_eof

        pid = self.pid

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "terminalId": terminal_id,
                "state": state,
                "exitCode": exit_code,
                "signal": signal,
                "topLevelExited": top_level_exited,
                "treeEmpty": tree_empty,
                "outputOffset": output_offset,
                "outputRetainedFrom": output_retained_from,
                "outputEof": output_eof,
            }
        )
        if pid is not UNSET:
            field_dict["pid"] = pid

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        terminal_id = d.pop("terminalId")

        state = ManagedTerminalStatusState(d.pop("state"))

        def _parse_exit_code(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        exit_code = _parse_exit_code(d.pop("exitCode"))

        def _parse_signal(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        signal = _parse_signal(d.pop("signal"))

        top_level_exited = d.pop("topLevelExited")

        tree_empty = d.pop("treeEmpty")

        output_offset = d.pop("outputOffset")

        output_retained_from = d.pop("outputRetainedFrom")

        output_eof = d.pop("outputEof")

        pid = d.pop("pid", UNSET)

        managed_terminal_status = cls(
            terminal_id=terminal_id,
            state=state,
            exit_code=exit_code,
            signal=signal,
            top_level_exited=top_level_exited,
            tree_empty=tree_empty,
            output_offset=output_offset,
            output_retained_from=output_retained_from,
            output_eof=output_eof,
            pid=pid,
        )

        managed_terminal_status.additional_properties = d
        return managed_terminal_status

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
