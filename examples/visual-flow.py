#!/usr/bin/env python3
"""Explicit CLI client: save an observation, or click one image candidate once.

No model/OCR service, credentials, desktop inspection, or implicit input retries.
"""
import argparse
import base64
import contextlib
import json
import os
import pathlib
import subprocess
import sys
import tempfile


def parser():
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument("--bridge", default="starbridge")
    commands = cli.add_subparsers(dest="command", required=True)
    observe = commands.add_parser("observe")
    observe.add_argument("vm")
    observe.add_argument("--hwnd", required=True)
    observe.add_argument("--pid", type=int, required=True)
    observe.add_argument("--start-time-utc", required=True)
    click = commands.add_parser("click")
    click.add_argument("vm")
    click.add_argument("--observation", type=pathlib.Path, required=True)
    for key in ("x", "y", "width", "height", "image-width", "image-height"):
        click.add_argument("--" + key, type=int, required=key in ("x", "y"))
    click.add_argument("--focus", action="store_true")
    click.add_argument("--strict-image-match", action="store_true")
    click.add_argument("--max-age-ms", type=int, default=15000)
    for command in (observe, click):
        command.add_argument("--output", type=pathlib.Path, required=True)
        command.add_argument("--no-uia", action="store_true")
    return cli


def parameters(args):
    if args.command == "observe":
        return dict(hwnd=args.hwnd, pid=args.pid, start_time_utc=args.start_time_utc,
                    include_uia=not args.no_uia)
    with args.observation.open("rb") as file:
        raw = file.read(8 * 1024 * 1024 + 1)
    if len(raw) > 8 * 1024 * 1024:
        raise ValueError("Observation file exceeds the protocol response budget.")
    before = json.loads(raw)
    if before.get("ok") is not True:
        raise ValueError("A successful ui.observe/ui.click result is required.")
    target = {key: getattr(args, key) for key in
              ("x", "y", "width", "height", "image_width", "image_height")
              if getattr(args, key) is not None}
    return dict(observation=before["data"]["observation"], target=target,
                focus=args.focus, strict_image_match=args.strict_image_match,
                max_age_ms=args.max_age_ms, include_uia=not args.no_uia)


def main():
    args = parser().parse_args()
    request = parameters(args)
    # Reserve private, exclusive output files before a possible click. Refuse
    # existing directories instead of overwriting an earlier observation.
    args.output.mkdir(mode=0o700, parents=True, exist_ok=False)
    # The only input command is this explicit click subcommand. The bridge
    # provides mutation authorization, token/geometry guards and no retries.
    with contextlib.ExitStack() as output:
        reserved = {}
        for name in ("result.json", "screenshot.png"):
            descriptor = os.open(args.output / name, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            reserved[name] = output.enter_context(os.fdopen(descriptor, "wb"))
        with tempfile.TemporaryDirectory(prefix="starbridge-visual-") as temporary:
            path = pathlib.Path(temporary) / "request.json"
            path.write_text(json.dumps(request), encoding="utf-8")
            argv = [args.bridge, "dev", "ui." + args.command, args.vm, "--request", str(path)]
            if args.command == "click":
                argv.append("--allow-write")
            process = subprocess.run(argv, stdout=subprocess.PIPE, check=False, timeout=40)
        result = json.loads(process.stdout)
        reserved["result.json"].write(json.dumps(result, ensure_ascii=False).encode("utf-8"))
        if result.get("ok") is True:
            pixels = base64.b64decode(result["data"]["screenshot"]["png_base64"], validate=True)
            reserved["screenshot.png"].write(pixels)
            print("Saved fresh observation and screenshot to", args.output)
            if args.command == "click":
                print("Input dispatched; business verification is pending. Inspect the new observation.")
        else:
            reserved["screenshot.png"].close()
            (args.output / "screenshot.png").unlink()
            print("Bridge rejected or could not confirm the operation; inspect result.json before retrying.", file=sys.stderr)
    return process.returncode or (0 if result.get("ok") is True else 1)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, KeyError, subprocess.TimeoutExpired) as error:
        print("Visual client could not complete. Inspect application state before retrying a click:", type(error).__name__, file=sys.stderr)
        sys.exit(1)
