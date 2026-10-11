#!/usr/bin/env python3
"""Exercise the real Compose deployment using an isolated database and data directory."""
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def main():
    image = sys.argv[1] if len(sys.argv) == 2 else "artemis:ci"
    image_name, tag = image.rsplit(":", 1)
    project = "artemis-smoke-" + secrets.token_hex(6)
    # A complex password catches regressions to unescaped URI concatenation.
    password = secrets.token_hex(24) + ':/$@#"\\\' '
    env = {k: v for k, v in os.environ.items() if not k.startswith(("ARTEX_", "POSTGRES_", "COMPOSE_"))}
    env.update(ARTEX_IMAGE=image_name, ARTEX_TAG=tag, ARTEX_BIND="127.0.0.1",
               ARTEX_PORT="0", ARTEX_PROXY_PORT="0", POSTGRES_PASSWORD=password,
               ANTHROPIC_API_KEY="", OPENAI_API_KEY="")
    with tempfile.TemporaryDirectory(prefix="artemis-smoke-") as directory:
        shutil.copyfile(ROOT / "docker-compose.yml", Path(directory) / "compose.yml")
        shutil.copytree(ROOT / "skills", Path(directory) / "skills")
        # Named test storage avoids root-owned bind-mount files on the CI host.
        override = Path(directory) / "smoke.json"
        override.write_text(json.dumps({"services": {"artemis": {"volumes": ["smoke-data:/app/data"]}},
                                        "volumes": {"smoke-data": {}}}), encoding="utf-8")
        (Path(directory) / ".env").write_text("", encoding="utf-8")
        compose = ["docker", "compose", "--project-name", project, "--project-directory", directory,
                   "--env-file", str(Path(directory) / ".env"), "-f", str(Path(directory) / "compose.yml"),
                   "-f", str(override)]

        def run(*args, check=True):
            return subprocess.run(compose + list(args), env=env, check=check,
                                  capture_output=True, text=True, timeout=300)

        try:
            run("pull", "postgres")
            run("up", "-d", "--no-build", "--pull", "never", "--wait", "--wait-timeout", "180")
            address = run("port", "artemis", "8787").stdout.strip()
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

            def request(path, data=None, token=None, expected=200, raw=False):
                headers = {"Content-Type": "application/json"}
                if token:
                    headers["Authorization"] = "Bearer " + token
                req = urllib.request.Request("http://" + address + path,
                    data=None if data is None else json.dumps(data).encode(), headers=headers)
                try:
                    response = opener.open(req, timeout=10)
                except urllib.error.HTTPError as error:
                    response = error
                with response:
                    body = response.read()
                    assert response.status == expected, f"{path}: HTTP {response.status}, expected {expected}"
                return body if raw else json.loads(body)

            assert b"<!doctype html" in request("/setup", raw=True).lower(), "Embedded UI missing"
            assert request("/api/auth/status")["initialized"] is False
            request("/api/tasks", expected=401)
            admin_password = secrets.token_hex(24)
            token = request("/api/auth/init", {"password": admin_password})["token"]
            assert request("/api/auth/status")["initialized"] is True
            request("/api/auth/init", {"password": admin_password}, expected=403)
            request("/api/auth/login", {"username": "artemis", "password": "wrong-password"}, expected=401)
            token = request("/api/auth/login", {"username": "artemis", "password": admin_password})["token"]
            assert not request("/api/tasks", token=token)["tasks"]
            assert not request("/api/companies", token=token)
            assert request("/api/assets", token=token)["total"] == 0
            assert request("/api/exploration/findings?page=1", token=token)["total"] == 0
            assert not request("/api/llm/profiles", token=token)["profiles"]
            # Recreate the application to prove auth/JWT persistence in mounted data and PG.
            run("up", "-d", "--no-deps", "--force-recreate", "--no-build", "--pull", "never",
                "--wait", "--wait-timeout", "180", "artemis")
            address = run("port", "artemis", "8787").stdout.strip()
            assert request("/api/auth/status")["initialized"] is True
            assert not request("/api/tasks", token=token)["tasks"]
            print("PASS: embedded UI, initialization, auth, empty data, special-character DB password, persistence")
        except Exception:
            # Avoid dumping credentials/Compose configuration; service logs contain no supplied keys.
            print(run("logs", "--no-color", "--tail", "100", check=False).stdout, file=sys.stderr)
            raise
        finally:
            # Only this random test project's containers, anonymous volumes and PG volume are removed.
            run("down", "--volumes", "--remove-orphans", check=False)


if __name__ == "__main__":
    main()
