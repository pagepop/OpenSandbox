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

from ..models.managed_process_status_state import ManagedProcessStatusState
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedProcessStatus")


@_attrs_define
class ManagedProcessStatus:
    """
    Attributes:
        process_id (str):
        state (ManagedProcessStatusState):
        exit_code (int | None):
        signal (None | str):
        top_level_exited (bool):
        tree_empty (bool):
        stdin_sequence (int):
        stdout_offset (int):
        stderr_offset (int):
        stdout_retained_from (int):
        stderr_retained_from (int):
        stdout_spill_path (None | str):
        stderr_spill_path (None | str):
        pid (int | Unset): Diagnostic operating-system PID, present after publication
    """

    process_id: str
    state: ManagedProcessStatusState
    exit_code: int | None
    signal: None | str
    top_level_exited: bool
    tree_empty: bool
    stdin_sequence: int
    stdout_offset: int
    stderr_offset: int
    stdout_retained_from: int
    stderr_retained_from: int
    stdout_spill_path: None | str
    stderr_spill_path: None | str
    pid: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        process_id = self.process_id

        state = self.state.value

        exit_code: int | None
        exit_code = self.exit_code

        signal: None | str
        signal = self.signal

        top_level_exited = self.top_level_exited

        tree_empty = self.tree_empty

        stdin_sequence = self.stdin_sequence

        stdout_offset = self.stdout_offset

        stderr_offset = self.stderr_offset

        stdout_retained_from = self.stdout_retained_from

        stderr_retained_from = self.stderr_retained_from

        stdout_spill_path: None | str
        stdout_spill_path = self.stdout_spill_path

        stderr_spill_path: None | str
        stderr_spill_path = self.stderr_spill_path

        pid = self.pid

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "processId": process_id,
                "state": state,
                "exitCode": exit_code,
                "signal": signal,
                "topLevelExited": top_level_exited,
                "treeEmpty": tree_empty,
                "stdinSequence": stdin_sequence,
                "stdoutOffset": stdout_offset,
                "stderrOffset": stderr_offset,
                "stdoutRetainedFrom": stdout_retained_from,
                "stderrRetainedFrom": stderr_retained_from,
                "stdoutSpillPath": stdout_spill_path,
                "stderrSpillPath": stderr_spill_path,
            }
        )
        if pid is not UNSET:
            field_dict["pid"] = pid

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        process_id = d.pop("processId")

        state = ManagedProcessStatusState(d.pop("state"))

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

        stdin_sequence = d.pop("stdinSequence")

        stdout_offset = d.pop("stdoutOffset")

        stderr_offset = d.pop("stderrOffset")

        stdout_retained_from = d.pop("stdoutRetainedFrom")

        stderr_retained_from = d.pop("stderrRetainedFrom")

        def _parse_stdout_spill_path(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        stdout_spill_path = _parse_stdout_spill_path(d.pop("stdoutSpillPath"))

        def _parse_stderr_spill_path(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        stderr_spill_path = _parse_stderr_spill_path(d.pop("stderrSpillPath"))

        pid = d.pop("pid", UNSET)

        managed_process_status = cls(
            process_id=process_id,
            state=state,
            exit_code=exit_code,
            signal=signal,
            top_level_exited=top_level_exited,
            tree_empty=tree_empty,
            stdin_sequence=stdin_sequence,
            stdout_offset=stdout_offset,
            stderr_offset=stderr_offset,
            stdout_retained_from=stdout_retained_from,
            stderr_retained_from=stderr_retained_from,
            stdout_spill_path=stdout_spill_path,
            stderr_spill_path=stderr_spill_path,
            pid=pid,
        )

        managed_process_status.additional_properties = d
        return managed_process_status

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
