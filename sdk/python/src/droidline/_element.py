"""Element objects returned by find() and find_all()."""

from __future__ import annotations

from typing import TYPE_CHECKING, Any, Dict, List, Mapping, Optional, Tuple, Union

if TYPE_CHECKING:
    from ._generated import By, DeviceCommands, InputResult, Query, TouchResult


class Element:
    """One screen element, as it was when find() or find_all() returned it.

    The fields are a snapshot. Actions look the element up again by its exact bounds and
    class, so after it moved or disappeared they raise NotFoundError; call refresh() or
    find it again. Actions do not wait unless you pass timeout.
    """

    def __init__(self, device: DeviceCommands, node: Mapping[str, Any]) -> None:
        self.device = device
        self.node: Dict[str, Any] = dict(node)

    @property
    def text(self) -> str:
        return str(self.node.get("text") or "")

    @property
    def id(self) -> str:
        return str(self.node.get("id") or "")

    @property
    def desc(self) -> str:
        return str(self.node.get("desc") or "")

    @property
    def class_name(self) -> str:
        return str(self.node.get("class") or "")

    @property
    def package(self) -> str:
        return str(self.node.get("package") or "")

    @property
    def bounds(self) -> Tuple[int, int, int, int]:
        b = list(self.node.get("bounds") or [0, 0, 0, 0])
        return (int(b[0]), int(b[1]), int(b[2]), int(b[3]))

    @property
    def center(self) -> Tuple[int, int]:
        left, top, right, bottom = self.bounds
        return ((left + right) // 2, (top + bottom) // 2)

    def __getitem__(self, field: str) -> Any:
        """Any node field, such as element["checked"] or element["scrollable"]."""
        return self.node[field]

    def query(self) -> Dict[str, Any]:
        """The query that finds this element again: its exact bounds and class."""
        q: Dict[str, Any] = {"bounds": list(self.bounds)}
        if self.class_name:
            q["class"] = self.class_name
        return q

    def click(self, timeout: float = 0) -> TouchResult:
        return self.device.touch(self.query(), timeout=timeout)

    def long_click(self, ms: Optional[int] = None, timeout: float = 0) -> TouchResult:
        return self.device.long_touch(self.query(), ms=ms, timeout=timeout)

    def input(self, text: str, append: Optional[bool] = None, timeout: float = 0) -> InputResult:
        return self.device.input(self.query(), text=text, append=append, timeout=timeout)

    def clear(self, timeout: float = 0) -> Any:
        return self.device.clear(self.query(), timeout=timeout)

    def tap(self) -> Any:
        """Tap the center point, without looking the element up again."""
        x, y = self.center
        return self.device.tap(x, y)

    def exists(self) -> bool:
        return bool(self.device.exists(self.query()))

    def refresh(self, timeout: float = 0) -> Element:
        """The same element with its current fields, for example after its text changed."""
        return self.device.find(self.query(), timeout=timeout)

    def find(self, by: Union[By, Query], value: Optional[str] = None, nth: Optional[int] = None, timeout: Optional[float] = None) -> Element:
        """An element inside this one."""
        return self.device.find(self._within(by, value), nth=nth, timeout=timeout)

    def find_all(self, by: Union[By, Query], value: Optional[str] = None, timeout: Optional[float] = None) -> List[Element]:
        """Every element inside this one that matches."""
        return self.device.find_all(self._within(by, value), timeout=timeout)

    def _within(self, by: Union[str, Mapping[str, Any]], value: Optional[str]) -> Dict[str, Any]:
        q: Dict[str, Any] = dict(by) if isinstance(by, Mapping) else {by: value}
        if "inside" in q:
            raise ValueError("this query already has inside; call device.find() with your own query instead")
        q["inside"] = self.query()
        return q

    def __eq__(self, other: object) -> bool:
        return isinstance(other, Element) and other.bounds == self.bounds and other.class_name == self.class_name

    def __hash__(self) -> int:
        return hash((self.bounds, self.class_name))

    def __repr__(self) -> str:
        label = self.text or self.desc or self.id
        return f"Element({self.class_name or '?'} {label!r} {list(self.bounds)})"
