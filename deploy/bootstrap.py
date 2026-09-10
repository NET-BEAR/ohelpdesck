"""Generate local-only credentials without printing them or replacing existing values."""
import os
import secrets
from pathlib import Path

target = Path(os.environ.get("ENV_FILE", ".env"))
try:
    descriptor = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
except FileExistsError:
    print(f"Configuration already exists: {target}")
else:
    with os.fdopen(descriptor, "w") as output:
        output.write(f"POSTGRES_PASSWORD={secrets.token_hex(24)}\n")
        output.write(f"S3_ACCESS_KEY={secrets.token_hex(12)}\n")
        output.write(f"S3_SECRET_KEY={secrets.token_hex(24)}\n")
    print(f"Generated private development configuration: {target}")
