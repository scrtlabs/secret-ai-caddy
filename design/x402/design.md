# x402 Support in SecretVM Reverse Proxy

This design document reflects the SecretVM x402 details that matter for a Caddy integration: agent-authenticated calls use x-agent-address, x-agent-signature, and x-agent-timestamp; signatures are based on METHOD + PATH + BODY + TIMESTAMP; POST /api/agent/add-funds can return 402 and then be retried with payment-signature or x-payment; and the standard agent flow is top up → balance → create VM → poll status.  ￼

1) Proposed Caddy architecture for metering and x402 support
```mermaid
flowchart TB
    C[Client / Agent SDK] --> P[Portal UI + Wallet Logic]
    C --> G[Caddy Gateway with Custom Module]

    subgraph Edge["Caddy Edge"]
        G --> A[Auth Verifier\nx-agent headers]
        A --> Q[Pricing / Quote Engine]
        Q --> R[Reservation Manager]
        R --> M[Usage Metering]
        M --> S[Settlement Engine]
        S --> U[Upstream Router]
        S --> E[Usage Event Log / Audit Trail]
        R --> L[Spendable Ledger Cache]
        S --> L
        G --> X[402 Challenge Builder]
    end

    U --> AI[AI App / Model API / Backend]

    subgraph Control["Billing / Funding Control Plane"]
        P --> W[x402 Client]
        W --> SV[SecretVM x402 API]
        P --> B[Billing Backend / Reconciler]
        B --> L
        B --> E
        B --> SV
    end

    G -. insufficient balance .-> X
    X -. 402 Payment Required .-> C

    P -. top up / balance checks .-> SV
    B -. sync funded balance / reconcile .-> SV

    classDef edge fill:#eef,stroke:#335,stroke-width:1px;
    classDef control fill:#efe,stroke:#353,stroke-width:1px;
    classDef external fill:#f8f8f8,stroke:#666,stroke-width:1px;

    class G,A,Q,R,M,S,U,L,E,X edge;
    class P,W,B,SV control;
    class C,AI external;
```

> Notes
	•	Portal remains the funding authority and wallet owner.
	•	Caddy handles hot-path enforcement: authenticate, price, reserve, meter, settle.
	•	Spendable ledger cache is the runtime authorization source so Caddy does not need to call SecretVM balance on every request.
	•	Billing backend / reconciler syncs top-ups and reconciles settled usage against SecretVM-backed funds.
	•	402 challenge builder lets Caddy reject underfunded requests cleanly without embedding full wallet UX into the proxy.

2) Sequence diagram: normal metered request through Caddy

This shows the prepaid authorization pattern for AI calls.
```mermaid
sequenceDiagram
    autonumber
    participant Client as Client / Agent
    participant Caddy as Caddy Module
    participant Ledger as Spendable Ledger
    participant Upstream as AI App / Model API
    participant Meter as Usage Meter
    participant Audit as Usage Log

    Client->>Caddy: POST /ai/endpoint\nx-agent-address\nx-agent-signature\nx-agent-timestamp
    Caddy->>Caddy: Verify agent signature\n(hash METHOD + PATH + BODY + TIMESTAMP)
    Caddy->>Caddy: Compute quote / estimate
    Caddy->>Ledger: Reserve estimated spend
    alt Sufficient balance
        Ledger-->>Caddy: Reservation created
        Caddy->>Upstream: Forward request
        Upstream-->>Caddy: Response / stream
        Caddy->>Meter: Compute actual usage
        Meter-->>Caddy: Actual cost
        Caddy->>Ledger: Finalize settlement\n(charge actual, refund unused reserve)
        Ledger-->>Caddy: Settled
        Caddy->>Audit: Write immutable usage event
        Caddy-->>Client: 200 OK / streamed response
    else Insufficient balance
        Ledger-->>Caddy: Reserve denied
        Caddy-->>Client: 402 Payment Required\n(machine-readable challenge)
    end
```
This aligns with the SecretVM signing model for agent-authenticated requests, including replay protection expectations tied to fresh timestamps and per-request hashes.  ￼

3) Sequence diagram: x402 top-up flow triggered by a Caddy 402

This is the cleaner split where Caddy does not perform wallet payment itself.
```mermaid
sequenceDiagram
    autonumber
    participant Client as Client / Agent
    participant Caddy as Caddy Module
    participant Portal as Portal + Wallet Logic
    participant X402 as x402 Client
    participant SecretVM as SecretVM API
    participant Billing as Billing Backend
    participant Ledger as Spendable Ledger

    Client->>Caddy: POST /ai/endpoint
    Caddy->>Ledger: Reserve estimated spend
    Ledger-->>Caddy: Insufficient balance
    Caddy-->>Client: 402 Payment Required\nincludes funding instructions / challenge ref

    Client->>Portal: Resolve payment requirement
    Portal->>SecretVM: POST /api/agent/add-funds\nx-agent-* headers
    SecretVM-->>Portal: 402 with payment details
    Portal->>X402: Generate payment artifact
    X402-->>Portal: payment-signature / x-payment
    Portal->>SecretVM: Retry POST /api/agent/add-funds\n+ payment-signature or x-payment
    SecretVM-->>Portal: 200 OK + updated balance

    Portal->>Billing: Notify funded amount / credit event
    Billing->>Ledger: Credit spendable balance
    Ledger-->>Billing: Credited

    Client->>Caddy: Retry original POST /ai/endpoint
    Caddy->>Ledger: Reserve estimated spend
    Ledger-->>Caddy: Reservation created
    Caddy-->>Client: Request proceeds
```
The top-up retry behavior here follows the documented POST /api/agent/add-funds flow: first call may return 402, and the retry includes payment-signature or x-payment.  ￼

4) Sequence diagram: direct SecretVM agent flow from Caddy for balance and VM lifecycle

When the module needs to speak to the SecretVM agent API directly, this shows the call pattern.
```mermaid
sequenceDiagram
    autonumber
    participant Caddy as Caddy Billing / Control Client
    participant Signer as Agent Request Signer
    participant SecretVM as SecretVM API

    Caddy->>Signer: Build signed request\nmethod, path-only, exact body, timestamp
    Signer-->>Caddy: x-agent-address\nx-agent-signature\nx-agent-timestamp

    Caddy->>SecretVM: GET /api/agent/balance\nx-agent-* headers
    SecretVM-->>Caddy: 200 { balance, ... }

    Caddy->>Signer: Build multipart canonical body\nstable JSON of fields + file metadata
    Signer-->>Caddy: x-agent-* headers for create-vm

    Caddy->>SecretVM: POST /api/vm/create\nmultipart/form-data + x-agent-* headers
    SecretVM-->>Caddy: 200 { id, name, vmDomain, ... }

    loop Poll until terminal state
        Caddy->>Signer: Build signed GET for /api/agent/vm/:id
        Signer-->>Caddy: x-agent-* headers
        Caddy->>SecretVM: GET /api/agent/vm/:id
        SecretVM-->>Caddy: 200 { status, ... }
    end
```
This reflects the documented requirements that:
	•	the signed path is the path only, without query string,
	•	the body must match exactly,
	•	JSON should be stable/sorted,
	•	POST /api/vm/create signs a stable JSON representation of form fields plus file metadata rather than raw multipart bytes.  ￼

1) Sequence diagram: streaming AI request with reserve and final settlement

For token-metered AI endpoints, this is usually the most important runtime flow.
```mermaid
sequenceDiagram
    autonumber
    participant Client as Client
    participant Caddy as Caddy Module
    participant Ledger as Spendable Ledger
    participant Upstream as Streaming Model API
    participant Meter as Stream Meter
    participant Audit as Usage Log

    Client->>Caddy: POST /chat/completions
    Caddy->>Caddy: Verify x-agent auth
    Caddy->>Caddy: Estimate input + max output budget
    Caddy->>Ledger: Reserve spend
    alt Reservation approved
        Ledger-->>Caddy: Reservation ID
        Caddy->>Upstream: Forward streaming request
        loop For each chunk
            Upstream-->>Caddy: Stream chunk / token data
            Caddy->>Meter: Increment usage counters
            Caddy-->>Client: Forward chunk
        end
        Upstream-->>Caddy: Stream end
        Caddy->>Meter: Compute final actual usage
        Meter-->>Caddy: Final token cost
        Caddy->>Ledger: Settle reservation
        Caddy->>Audit: Persist usage event
        Caddy-->>Client: Stream complete
    else Reservation denied
        Ledger-->>Caddy: Insufficient balance
        Caddy-->>Client: 402 Payment Required
    end
```
Suggested naming inside the Caddy module

For the implementation, we will align the module boundaries with the diagrams:
```
caddy.x402
├── AuthVerifier
├── QuoteEngine
├── ReservationStore
├── SpendableLedger
├── UsageMeter
├── SettlementEngine
├── ChallengeBuilder
├── SecretVMClient
│   ├── BuildAgentHeaders()
│   ├── GetBalance()
│   ├── AddFunds()
│   ├── CreateVM()
│   └── GetVMStatus()
└── Reconciler
```
Most important implementation constraint

Caddy makes SecretVM agent API calls itself, hence need to build signatures from the final outbound request after all mutations. The docs are explicit that the signed payload depends on exact method, exact path, exact body, and a fresh timestamp; replays are rejected.  ￼
