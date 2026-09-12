"""Check the real contract and prove that the validator rejects an invalid document."""
from copy import deepcopy
from openapi_spec_validator import validate
from openapi_spec_validator.readers import read_from_filename

document, _ = read_from_filename("api/openapi.yaml")
validate(document)
invalid = deepcopy(document)
invalid["openapi"] = "invalid-version"
try:
    validate(invalid)
except Exception:
    print("OpenAPI valid; negative validator control rejected")
else:
    raise SystemExit("Validator failed negative control")
