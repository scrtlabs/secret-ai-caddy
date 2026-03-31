# Internal Dev Memo: Optional Permit File and Master Keys File

## Summary

`permit_file` and `master_keys_file` are now **optional** Caddyfile directives. Both can be replaced entirely by environment variables, simplifying deployment and eliminating the need to mount secret files into containers.

---

## Permit File

### What changed

The `permit_file` directive pointed to a JSON file containing the Secret Network permit configuration used to retrieve Secret AI API Keys from the KMS contract. Previously, if no file was specified, the system fell back to a hardcoded default permit. That hardcoded default has been removed.

Now, if `permit_file` is not configured, the system constructs the permit on the fly from three required environment variables:

| Env Var | Description | Example |
|---------|-------------|---------|
| `SECRETAI_PERMIT_TYPE` | Public key type | `tendermint/PubKeySecp256k1` |
| `SECRETAI_PERMIT_PUBKEY` | Public key value | `Aur9D8RLqYMf3sBTiXdhH8mMI9bPHisdDa9y9jwW9RyT` |
| `SECRETAI_PERMIT_SIG` | Permit signature | `TeNtblPmooqErLX3ECMKfAYJdVB5/BTULW00CGX5nDZ1...` |

### Validation behavior

- If `permit_file` is set: the file is used as before, env vars are ignored for permit construction.
- If `permit_file` is not set and all three env vars are present: a permit is constructed at runtime.
- If `permit_file` is not set and any env var is missing: **Caddy fails to start** with a clear error listing which variables are missing.

### Code path

- `GetDefaultPermit()` in `validators/api_key_validator.go` reads the env vars and returns `(map, error)`.
- `Validate()` in `secret_reverse_proxy.go` checks at startup that either the file or all three env vars are present.

---

## Master Keys File

### What changed

The `master_keys_file` directive pointed to a local file containing one master API key per line. This file is now optional.

If `master_keys_file` is not configured, master keys can be provided via the `SECRETAI_MASTER_KEYS` environment variable as a **comma-separated list** of API keys.

| Env Var | Description | Example |
|---------|-------------|---------|
| `SECRETAI_MASTER_KEYS` | Comma-separated list of master API keys | `key1,key2,key3` |

### Validation behavior

- If `master_keys_file` is set: the file is checked first. If the file also doesn't contain the key, `SECRETAI_MASTER_KEYS` is checked as well (both sources are consulted).
- If `master_keys_file` is not set and `SECRETAI_MASTER_KEYS` is set: keys are checked against the env var list only.
- If neither is configured: no master key file/list validation occurs (requests fall through to contract-based validation).

### Code path

- `CheckMasterKeys()` in `validators/api_key_validator.go` checks the file first (if configured), then always checks `SECRETAI_MASTER_KEYS`.

---

## Caddyfile Examples

### Before (file-based, still supported)

From `Caddyfile-test`:

```caddyfile
secret_reverse_proxy {
    API_MASTER_KEY {env.SECRET_API_MASTER_KEY}
    master_keys_file /etc/caddy/master_keys.txt
    secret_node {env.SECRET_NODE}
    contract_address {env.SECRET_CONTRACT}
    secret_chain_id {env.SECRET_CHAIN_ID}
    permit_file /etc/caddy/permit.json
    metering {env.METERING}
    metering_interval {env.METERING_INTERVAL}
    metering_url {env.METERING_URL}
}
```

### After (env-var-based, no files needed)

```caddyfile
secret_reverse_proxy {
    API_MASTER_KEY {env.SECRET_API_MASTER_KEY}
    secret_node {env.SECRET_NODE}
    contract_address {env.SECRET_CONTRACT}
    secret_chain_id {env.SECRET_CHAIN_ID}
    metering {env.METERING}
    metering_interval {env.METERING_INTERVAL}
    metering_url {env.METERING_URL}
}
```

With the following env vars set:

```bash
SECRETAI_MASTER_KEYS="key1,key2,key3"
SECRETAI_PERMIT_TYPE="tendermint/PubKeySecp256k1"
SECRETAI_PERMIT_PUBKEY="Aur9D8RLqYMf3sBTiXdhH8mMI9bPHisdDa9y9jwW9RyT"
SECRETAI_PERMIT_SIG="TeNtblPmooqErLX3ECMKfAYJdVB5/BTULW00CGX5nDZ1..."
```

### Mixed (file for permit, env var for master keys)

Both approaches can be mixed. For example, use `permit_file` from a mounted secret but provide master keys via env var, or vice versa.

---

## Logging

Log output now shows the source of each optional config parameter:

- **File configured**: `master_keys=/etc/caddy/master_keys.txt`
- **Env var in use**: `master_keys=(using env: SECRETAI_MASTER_KEYS)`
- **Neither**: `master_keys=(not configured)`

Same pattern applies to `permit` with its three env vars.

---

## Docker Deployment Impact

This change simplifies container deployments by removing the need to volume-mount `permit.json` and `master_keys.txt`. All configuration can now flow through environment variables or orchestrator secrets (e.g., Kubernetes Secrets, Docker Swarm secrets injected as env vars).
