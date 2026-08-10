#!/usr/bin/env python3
"""Reject unsafe tar members before a backup archive is extracted."""
import pathlib
import sys
import tarfile


def fail(message: str) -> None:
    raise SystemExit(f"unsafe backup archive: {message}")


with tarfile.open(sys.argv[1], "r:gz") as archive:
    for member in archive.getmembers():
        path = pathlib.PurePosixPath(member.name)
        if path.is_absolute() or not member.name or ".." in path.parts:
            fail(f"invalid member path {member.name!r}")
        if member.isdev() or member.isfifo():
            fail(f"special file {member.name!r}")
        if member.issym() or member.islnk():
            target = pathlib.PurePosixPath(member.linkname)
            if target.is_absolute() or ".." in target.parts:
                fail(f"unsafe link {member.name!r} -> {member.linkname!r}")
