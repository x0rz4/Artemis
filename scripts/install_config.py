#!/usr/bin/env python3
"""Write a new local config without shell/JSON interpolation or overwriting secrets."""
import json
import os
from pathlib import Path
import sys


def main():
    if sys.argv[1:] == ["--compose"]:
        service = json.load(sys.stdin)["services"]["postgres"]
        # `compose config` produces a reusable Compose document, escaping every
        # literal dollar as $$. Undo that serialization before writing app JSON.
        env = {key: value.replace("$$", "$") for key, value in service["environment"].items()}
        port = next(p["published"] for p in service["ports"] if p["target"] == 5432)
        database = dict(host="127.0.0.1", port=int(port), user=env["POSTGRES_USER"],
                        password=env["POSTGRES_PASSWORD"], dbname=env["POSTGRES_DB"], sslmode="disable")
    elif not sys.argv[1:]:
        database = dict(host=os.environ["POSTGRES_HOST"], port=int(os.environ["POSTGRES_PORT"]),
                        user=os.environ["POSTGRES_USER"], password=os.environ["POSTGRES_PASSWORD"],
                        dbname=os.environ["POSTGRES_DB"], sslmode=os.environ["POSTGRES_SSLMODE"])
    else:
        raise SystemExit("usage: install_config.py [--compose]")
    if not 1 <= database["port"] <= 65535:
        raise SystemExit("PostgreSQL 端口必须在 1..65535")
    if database["sslmode"] not in {"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}:
        raise SystemExit("无效 sslmode")
    if not all(database[key] for key in ("host", "user", "password", "dbname")):
        raise SystemExit("数据库地址、用户、密码、库名不能为空")
    payload = json.dumps({"database": database, "skill_dir": ""}, ensure_ascii=False, indent=2) + "\n"
    # Exclusive creation also protects against an installer race.
    fd = os.open(Path("config.json"), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as config:
        config.write(payload)


if __name__ == "__main__":
    main()
