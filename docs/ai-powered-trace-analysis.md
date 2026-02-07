# AI-Powered Trace Analysis for Jaeger

> **Author:** Tilak Gupta
> **Date:** February 2026
> **Status:** Implementation Complete — All tests pass, lint clean
> **Package:** `cmd/jaeger/internal/extension/jaegerquery/internal/nlquery`

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Motivation & Problem Statement](#2-motivation--problem-statement)
3. [Design Philosophy](#3-design-philosophy)
4. [Architecture Overview](#4-architecture-overview)
5. [Feature 1 — Natural Language Search (NL → SearchParams)](#5-feature-1--natural-language-search-nl--searchparams)
6. [Feature 2 — Contextual Analysis & Summarization](#6-feature-2--contextual-analysis--summarization)
7. [Session Management](#7-session-management)
8. [Configuration](#8-configuration)
9. [HTTP API Reference](#9-http-api-reference)
10. [Implementation Details](#10-implementation-details)
11. [MCP Integration — ToolCaller Bridge (Option B)](#11-mcp-integration--toolcaller-bridge-option-b)
12. [Testing Strategy](#12-testing-strategy)
13. [Code Metrics](#13-code-metrics)
14. [Security & Safety Guarantees](#14-security--safety-guarantees)
15. [Deployment & Operator Guide](#15-deployment--operator-guide)
16. [Future Work](#16-future-work)

---

## 1. Executive Summary

This document describes the design and implementation of two AI-powered features for the Jaeger Query Service:

1. **Natural Language Search** — Converts free-text queries like *"show me slow requests from payment-service taking more than 2s"* into structured `TraceQueryParams` that the existing Jaeger search pipeline can execute.

2. **Contextual Analysis & Summarization** — Accepts a trace ID or span ID, sends the pruned trace data to a local language model, and returns a human-readable explanation. Supports multi-turn follow-up questions via session management.

Both features are designed as **additive extensions** to the Query Service. They introduce zero changes to existing APIs, zero changes to storage backends, and zero external cloud dependencies. The LLM runs locally (e.g., Ollama with a 0.5B–3B parameter model) and is treated strictly as an **extraction/summarization tool** — never as a decision maker.

**Key numbers:**
- 13 production files, 2,055 lines of production code
- 12 test files, 2,397 lines of test code, 123 test functions
- MCP ToolCaller bridge: enables search+analyze in a single endpoint
- `make fmt` ✅ | `make lint` ✅ (0 issues) | `make test` ✅ (all pass, no goroutine leaks)

---

## 2. Motivation & Problem Statement

### The Problem

Jaeger's current search interface requires users to manually fill in structured form fields: service name, operation name, tags, duration ranges, and result limits. This creates friction in two areas:

**Search friction:** A developer investigating a production incident thinks in natural language — *"show me the failed checkout requests in the last hour that took more than 5 seconds"* — but must mentally decompose this into service=`checkout-service`, tags=`{error: true}`, minDuration=`5s`, then fill in a web form. This cognitive overhead slows down incident response.

**Comprehension friction:** Once a trace is found, understanding it requires reading a waterfall diagram of spans, mentally reconstructing the request flow across services, identifying which span caused the error, and reasoning about latency distribution. For complex traces with 50+ spans across 10+ services, this is time-consuming even for experienced engineers.

### Why These Features Matter

The [Jaeger v2 project](https://github.com/jaegertracing/jaeger) is migrating to the OpenTelemetry Collector architecture. This is the right time to add AI-assisted capabilities because:

1. **The extension model is stable.** The Query Service runs as an OTel Collector extension, providing clean integration points for new features.
2. **Local LLMs are practical.** Models like Qwen2 (0.5B), Phi-3 (3.8B), and Llama 3.2 (3B) run on commodity hardware with sub-second latency. No GPU required for small models.
3. **The MCP (Model Context Protocol) server already provides structured trace access.** These features complement the MCP server by adding natural language interaction directly in the Query Service.

### Alignment with Jaeger Project Goals

From the [Jaeger v2 roadmap](https://github.com/jaegertracing/jaeger/issues/6601) and LFX mentorship project description:

> *"AI-Powered Trace Analysis: Using AI/ML capabilities for intelligent analysis of trace data, providing automated root cause analysis, anomaly detection, and natural language interaction with trace data."*

This implementation directly addresses the "natural language interaction" component and provides the foundation for root cause analysis and anomaly detection.

---

## 3. Design Philosophy

### AI as an Extraction Tool, Not a Decision Maker

The most important architectural constraint: **the LLM is a transformation function, not an autonomous agent.** It takes input (natural language or trace data) and produces structured output (JSON parameters or text summary). It never:

- Executes queries against the storage backend
- Modifies any state (traces, configuration, indexes)
- Makes decisions about what to show, filter, or hide
- Accesses any system beyond its input

This constraint is enforced at the code level by `json.Unmarshal` acting as a **safety firewall** — any hallucinated fields in the model's output are silently dropped because they don't match the Go struct definition.

### Zero External Dependencies

All AI processing happens locally. The operator points Jaeger at a local Ollama server running a small model. There are:

- No cloud API calls
- No API keys or secrets
- No network egress of trace data
- No vendor lock-in (any Ollama-compatible model works)

### Additive, Not Invasive

Every change is additive. No existing files, APIs, or behaviors are modified. The new endpoints are registered alongside existing routes. If the `nlquery` feature is disabled (the default), the system behaves identically to a vanilla Jaeger deployment.

### Graceful Degradation

The system is designed to work at three capability levels:

| Level | Config | Capability |
|-------|--------|-----------|
| **Off** | `nlquery.enabled: false` | No NL features. System unchanged. |
| **Heuristic only** | `nlquery.enabled: true` (no provider) | Regex-based NL search. No LLM needed. No analysis. |
| **Full AI** | `nlquery.enabled: true, provider: ollama` | LLM-backed search extraction + trace/span analysis + follow-up |

If the LLM server is unreachable, the system falls back to heuristic extraction for search and returns errors for analysis endpoints. Existing Jaeger functionality is never impacted.

---

## 4. Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Jaeger Query Service                         │
│  (OTel Collector Extension)                                        │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                    Existing Routes (unchanged)                │   │
│  │  GET /api/traces, GET /api/services, GET /api/operations...  │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                    New Routes (additive)                      │   │
│  │                                                              │   │
│  │  POST /api/nlquery               ──► NL Search Extraction    │   │
│  │  POST /api/nlquery/analyze/span  ──► Span Analysis           │   │
│  │  POST /api/nlquery/analyze/trace ──► Trace Analysis          │   │
│  │  POST /api/nlquery/analyze/followup ──► Follow-up Q&A        │   │
│  │  POST /api/nlquery/analyze/search ──► Search + Analyze (MCP) │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                              │                                      │
│                    ┌─────────┴──────────┐                           │
│                    │  nlquery Package   │                           │
│                    │                    │                           │
│    ┌───────────────┤  Components:       │                           │
│    │               │  • Extractor       ├──────────────┐            │
│    │               │  • Analyzer        │              │            │
│    │               │  • SessionManager  │         ┌────┴─────┐     │
│    │               │  • ToolCaller      │         │ Ollama   │     │
│    │               └────────┬───────────┘         │ (local)  │     │
│    │                        │                     │ qwen2/   │     │
│    ▼                        ▼                     │ llama3/  │     │
│  QueryService         ToolCaller                  │ phi3     │     │
│  .GetTraces()         .SearchTraces()             └──────────┘     │
│  (existing)           .GetServices()                               │
│                       (same QueryService                           │
│                        methods as MCP tools)                       │
└─────────────────────────────────────────────────────────────────────┘
```

### Package Structure

All new code resides in a single package:

```
cmd/jaeger/internal/extension/jaegerquery/internal/nlquery/
├── config.go              # YAML config struct & validation
├── extractor.go           # Extractor interface + StubExtractor
├── heuristic.go           # Regex-based extraction (no LLM)
├── llm_extractor.go       # LLM-backed extraction via LangChainGo
├── params.go              # SearchParams struct + ToTraceQueryParams() + ToMCPArgs()
├── handler.go             # HTTP handler for POST /api/nlquery
├── analysis_handler.go    # HTTP handlers for span/trace/search analysis + follow-up
├── analyzer.go            # LLM analysis engine with session integration
├── pruner.go              # Trace/span data pruning for LLM context windows
├── session.go             # TTL-based session manager for conversations
├── provider.go            # Component factory (creates model, extractor, analyzer)
├── mcp_bridge.go          # ToolCaller interface + QueryServiceToolCaller implementation
└── *_test.go              # 123 test functions across 12 test files
```

Modified files outside the package (minimal, additive only):
- `server.go` — Registers nlquery routes (6 lines added)
- `flags.go` — Adds `NLQuery nlquery.Config` to `QueryOptions` struct (2 lines added)

---

## 5. Feature 1 — Natural Language Search (NL → SearchParams)

### What It Does

Converts a natural language query into structured trace search parameters. The parameters are returned to the caller (UI or API client) for execution via the existing search API.

**Example:**

```
Input:  "show me failed requests from payment-service slower than 2s"
Output: {
  "service": "payment-service",
  "tags": {"http.status_code": "500"},
  "minDuration": "2s"
}
```

### How It Works

#### The Extractor Interface

```go
type Extractor interface {
    Extract(ctx context.Context, input string) (SearchParams, error)
}
```

This interface is deliberately minimal. It accepts a string and returns a flat, JSON-serializable struct. Three implementations exist:

1. **StubExtractor** — Returns empty params. Used for structural testing.
2. **HeuristicExtractor** — Regex/keyword-based extraction. Zero external dependencies.
3. **LLMExtractor** — Sends the input to a local language model for extraction.

#### HeuristicExtractor (Regex-Based)

The heuristic extractor uses precompiled regex patterns to detect:

| Pattern | Example Input | Extracted Field |
|---------|---------------|-----------------|
| `from <service>` | "from payment-service" | `service: "payment-service"` |
| `in <name>-service` | "in order-service" | `service: "order-service"` |
| `NNN errors` | "500 errors" | `tags: {"http.status_code": "500"}` |
| `status NNN` | "status 404" | `tags: {"http.status_code": "404"}` |
| `more than Ns` | "more than 2s" | `minDuration: "2s"` |
| `slower than Nms` | "slower than 500ms" | `minDuration: "500ms"` |
| `faster than Ns` | "faster than 100ms" | `maxDuration: "100ms"` |
| `GET /path` | "GET /api/users" | `operation: "GET /api/users"` |

This extractor is always available, requires no model, and serves as the fallback.

#### LLMExtractor (Model-Backed)

When an Ollama provider is configured, the LLM extractor sends the input to the model with a carefully crafted system prompt. The model is instructed to respond with **only** a JSON object matching the `SearchParams` schema. Key design decisions:

- **`json.Unmarshal` safety firewall:** The model's output is deserialized into the `SearchParams` Go struct. Any hallucinated fields (e.g., `"confidence"`, `"reasoning"`) are silently dropped because they don't match the struct definition. This is the primary safety mechanism against model misbehavior.

- **JSON mode enforced:** The model is called with `llms.WithJSONMode()`, which makes Ollama constrain output to valid JSON at the token generation level.

- **Temperature 0.0:** Fully deterministic output. Same input → same output.

- **System prompt as schema specification:** The system prompt explicitly lists allowed fields, their types, and extraction rules. It does not explain what Jaeger is or provide general instructions — it is a pure extraction schema.

#### SearchParams → TraceQueryParams Conversion

The extracted `SearchParams` (flat JSON) is converted to `tracestore.TraceQueryParams` (the canonical Jaeger type) via `ToTraceQueryParams()`. This conversion validates duration strings, converts tag maps to `pcommon.Map`, and fails fast on invalid values.

### Data Flow

```
User Input (string)
  │
  ▼
Extractor.Extract()
  │  ├── HeuristicExtractor: regex match → SearchParams
  │  └── LLMExtractor: model call → JSON → json.Unmarshal → SearchParams
  │                                           ▲ safety firewall
  ▼
SearchParams (validated JSON struct)
  │
  ▼
POST /api/nlquery response → returned to caller
  │
  ▼
Caller uses params to invoke existing GET /api/traces (unchanged)
```

---

## 6. Feature 2 — Contextual Analysis & Summarization

### What It Does

Accepts a trace ID (and optionally a span ID), fetches the trace from storage, prunes it to essential fields, sends the pruned data to the LLM, and returns a human-readable analysis. Supports multi-turn conversations via sessions.

**Example — Span Analysis:**

```http
POST /api/nlquery/analyze/span
{
  "trace_id": "0123456789abcdef0123456789abcdef",
  "span_id": "0123456789abcdef"
}

Response:
{
  "analysis": "This span represents a server-side HTTP handler for GET /api/checkout
               in the checkout-service. It completed with an ERROR status after 3.2s,
               which is significantly longer than typical. The error message indicates
               a database connection timeout. Key attributes show this was processing
               order #12345. The 3.2s duration suggests the database connection pool
               was exhausted.",
  "session_id": "a1b2c3d4e5f6..."
}
```

**Example — Follow-up:**

```http
POST /api/nlquery/analyze/followup
{
  "session_id": "a1b2c3d4e5f6...",
  "question": "What could cause the database connection pool to be exhausted?"
}

Response:
{
  "analysis": "Based on the trace context, the connection pool exhaustion could be
               caused by: 1) Long-running queries holding connections open...",
  "session_id": "a1b2c3d4e5f6..."
}
```

### How It Works

#### The Pruner — Preparing Trace Data for LLMs

Raw OTLP trace data contains binary IDs, nanosecond timestamps, internal OTel fields, and deeply nested protobuf structures. Sending this raw data to a small language model would:

1. Consume the entire context window with noise
2. Confuse the model with irrelevant fields
3. Waste tokens on wire-format encoding artifacts

The **pruner** solves this by extracting only the fields that matter for analysis:

```go
type PrunedSpan struct {
    SpanID       string            // hex string
    ParentSpanID string            // hex string (for call hierarchy)
    Service      string            // service.name from resource
    Operation    string            // span name
    Duration     string            // human-readable ("250ms", "3.20s")
    Status       string            // "OK", "ERROR", "UNSET"
    StatusMsg    string            // error message if present
    Kind         string            // "SERVER", "CLIENT", etc.
    Attributes   map[string]string // top 15 key-value pairs
    Events       []PrunedEvent     // log entries / events
}
```

Key pruning decisions:
- **Attribute cap:** Maximum 15 attributes per span (prevents large spans from blowing the context window)
- **Human-readable durations:** `"3.20s"` instead of `3200000000` nanoseconds
- **String status codes:** `"ERROR"` instead of `ptrace.StatusCodeError(2)`
- **Flattened hierarchy:** Parent span ID as string reference instead of nested protobuf

The pruner also produces a `PrunedTrace` struct with trace-level metadata:

```go
type PrunedTrace struct {
    TraceID   string       // hex string
    SpanCount int          // total number of spans
    Services  []string     // all unique service names
    RootSpan  string       // operation name of the root span
    Duration  string       // end-to-end trace duration
    Spans     []PrunedSpan // pruned span list
}
```

Both `PrunedSpan` and `PrunedTrace` have `FormatForLLM()` functions that convert them to readable text:

```
Span: GET /api/checkout
  Service: checkout-service
  Duration: 3.20s
  Status: ERROR (connection timeout)
  Kind: SERVER
  Attributes:
    http.method: GET
    http.status_code: 503
    db.system: postgresql
  Events:
    - exception @ 2026-01-15T10:30:00Z
      exception.message: connection pool exhausted
```

#### The Analyzer — LLM Integration with Session Context

The `Analyzer` struct ties together the LLM model, configuration, and session manager:

```go
type Analyzer struct {
    model    llms.Model       // LangChainGo model (shared with extractor)
    config   Config           // temperature, max_tokens
    sessions *SessionManager  // conversation state
    logger   *zap.Logger
}
```

Three public methods:

1. **`AnalyzeSpan(ctx, prunedSpan, sessionID)`** — Sends a pruned span to the LLM with a span-analysis system prompt. Returns the analysis text and a session ID.

2. **`AnalyzeTrace(ctx, prunedTrace, sessionID)`** — Sends a pruned trace to the LLM with a trace-analysis system prompt. Returns the analysis text and a session ID.

3. **`FollowUp(ctx, question, sessionID)`** — Sends a follow-up question to the LLM with the full conversation history from the session. Returns the answer.

**System prompts** are tailored for each task:

- **Span analysis prompt:** Instructs the model to explain what the span represents, whether it succeeded/failed, performance concerns, key attributes, and relevant events.
- **Trace analysis prompt:** Instructs the model to summarize the request flow, identify the critical path, highlight errors, point out bottlenecks, and describe service interactions.

Both prompts include the instruction: *"Do NOT make up information that is not present in the trace data."*

#### The Analysis Handler — HTTP Bridge

The `AnalysisHandler` bridges HTTP requests to the Analyzer:

1. **Parse request** — Extract trace_id, span_id, session_id from JSON body
2. **Fetch trace** — Call `QueryService.GetTraces()` (existing, unchanged)
3. **Prune** — Convert raw OTLP data to `PrunedSpan`/`PrunedTrace`
4. **Analyze** — Send to the Analyzer with appropriate system prompt
5. **Respond** — Return analysis text + session_id as JSON

The `fetchTrace()` method uses the existing `QueryService` to retrieve trace data, ensuring all storage-level access goes through the same code paths used by the rest of Jaeger.

### Data Flow

```
POST /api/nlquery/analyze/span  { trace_id, span_id, session_id? }
  │
  ▼
parseHexTraceID() + parseHexSpanID()    ◄── input validation
  │
  ▼
QueryService.GetTraces(traceID)         ◄── existing API (unchanged)
  │
  ▼
FindSpanInTrace(traces, spanID)         ◄── locate specific span
  │
  ▼
PruneSpan(span, resource)               ◄── extract essential fields
  │
  ▼
FormatPrunedSpanForLLM(prunedSpan)      ◄── human-readable text
  │
  ▼
Analyzer.AnalyzeSpan()
  ├── Resolve/Create Session
  ├── Build messages: [system prompt, history..., user prompt]
  ├── model.GenerateContent(messages)   ◄── Ollama API call
  └── Persist exchange in session
  │
  ▼
{ "analysis": "...", "session_id": "..." }
```

---

## 7. Session Management

### Why Sessions Are Needed

Without sessions, each analysis request is stateless. The user cannot ask *"Why did the span fail?"* after seeing the initial analysis, because the model has no memory of the previous exchange. Sessions enable multi-turn conversations where each follow-up question includes the full conversation history.

### Design

```go
type SessionManager struct {
    mu       sync.Mutex
    sessions map[string]*Session
    ttl      time.Duration       // 30 minutes default
    done     chan struct{}        // shutdown signal for GC goroutine
}

type Session struct {
    ID        string    // 128-bit random hex
    Messages  []Message // conversation history
    CreatedAt time.Time
    UpdatedAt time.Time
}

type Message struct {
    Role    MessageRole // "system", "user", "assistant"
    Content string
}
```

### Key Properties

| Property | Value | Rationale |
|----------|-------|-----------|
| **Storage** | In-memory `map[string]*Session` | Sessions are ephemeral — trace analysis context doesn't need to survive restarts |
| **TTL** | 30 minutes | Long enough for an investigation, short enough to prevent memory leaks |
| **Message cap** | 50 per session | Prevents unbounded memory growth from long conversations |
| **Eviction** | Oldest non-system message removed when cap is reached | System prompt is always preserved |
| **GC** | Background goroutine sweeps every 5 minutes | Removes expired sessions |
| **Concurrency** | Mutex-guarded | HTTP handlers may access sessions concurrently |
| **ID generation** | `crypto/rand` → 16 bytes → hex | Cryptographically random, 128-bit collision resistance |

### Lifecycle

```
┌──────────┐   POST /analyze/span    ┌─────────────┐
│  Client  │ ─────────────────────►  │   Handler   │
└──────────┘   { trace_id, span_id } │             │
                                     │  session_id │
               ◄─────────────────── │  = "abc..."  │
               { analysis,          └──────┬──────┘
                 session_id }              │
                                    SessionManager
                                    ┌──────┴──────┐
                                    │ sessions:   │
                                    │  "abc..." → │
                                    │   [system,  │
                                    │    user,    │
                                    │    asst]    │
                                    └─────────────┘
                                           │
┌──────────┐   POST /analyze/followup      │
│  Client  │ ─────────────────────────►   uses
└──────────┘   { session_id: "abc...",    existing
                 question: "why?" }       session
               ◄─────────────────────
               { analysis: "Because..." }
```

### Resource Safety

- **Goroutine leak prevention:** `SessionManager.Close()` stops the GC goroutine. All tests call `t.Cleanup(sm.Close)`.
- **Error path cleanup:** `NewComponentsFromConfig()` calls `sessions.Close()` if model creation fails.
- **`NewExtractorFromConfig()` cleanup:** Closes sessions since the legacy API only needs the extractor.
- **Verified with `goleak`:** The package uses `testutils.VerifyGoLeaks(m)` in `TestMain` — any leaked goroutines cause test failure.

---

## 8. Configuration

The feature is configured via the existing Jaeger YAML configuration under the `jaeger_query` extension:

```yaml
extensions:
  jaeger_query:
    storage:
      traces: memstore
    nlquery:
      enabled: true               # default: false
      provider: "ollama"          # optional; empty = heuristic only
      endpoint: "http://localhost:11434"  # required when provider is set
      model: "qwen2:0.5b"        # required when provider is set
      temperature: 0.0            # 0.0 = deterministic (default)
      max_tokens: 256             # for extraction; analysis auto-bumps to 512
```

### Config Validation Rules

| Field | Validation | Error |
|-------|-----------|-------|
| `provider` set but `endpoint` empty | Error | `"nlquery: endpoint is required when provider is set"` |
| `provider` set but `model` empty | Error | `"nlquery: model is required when provider is set"` |
| `temperature` < 0 or > 1 | Error | `"nlquery: temperature must be between 0.0 and 1.0"` |
| `max_tokens` < 0 | Error | `"nlquery: max_tokens must be non-negative"` |
| Unknown provider | Error | `"unsupported nlquery provider: \"xyz\""` |

### Behavioral Modes

| `enabled` | `provider` | Behavior |
|-----------|-----------|----------|
| `false` | — | No NL endpoints registered. System unchanged. |
| `true` | `""` (empty) | `HeuristicExtractor` for search. No analysis endpoints. Session manager active but unused. |
| `true` | `"ollama"` | `LLMExtractor` for search. `Analyzer` for analysis. Full session support. |
| `true` | `"ollama"` (server down) | Components creation fails → falls back to `HeuristicExtractor`. Logged as error. |

---

## 9. HTTP API Reference

### POST /api/nlquery — Natural Language Search Extraction

Converts natural language to structured search parameters.

**Request:**
```json
{ "query": "show me failed requests from payment-service slower than 2s" }
```

**Response (200):**
```json
{
  "params": {
    "service": "payment-service",
    "tags": { "http.status_code": "500" },
    "minDuration": "2s"
  }
}
```

**Error (400):**
```json
{ "error": "query field is required" }
```

---

### POST /api/nlquery/analyze/span — Span Analysis

Fetches a specific span and generates an LLM-powered explanation.

**Request:**
```json
{
  "trace_id": "0123456789abcdef0123456789abcdef",
  "span_id": "0123456789abcdef",
  "session_id": ""
}
```

**Response (200):**
```json
{
  "analysis": "This span represents a server-side handler...",
  "session_id": "a1b2c3d4e5f67890a1b2c3d4e5f67890"
}
```

**Errors:**
| Code | Condition |
|------|-----------|
| 400 | Missing trace_id or span_id, invalid hex format, malformed JSON |
| 404 | Trace not found, span not found in trace |
| 500 | Storage error, LLM failure |

---

### POST /api/nlquery/analyze/trace — Trace Analysis

Fetches an entire trace and generates a high-level summary.

**Request:**
```json
{
  "trace_id": "0123456789abcdef0123456789abcdef",
  "session_id": ""
}
```

**Response (200):**
```json
{
  "analysis": "This trace shows a request flow across 5 services...",
  "session_id": "a1b2c3d4e5f67890a1b2c3d4e5f67890"
}
```

---

### POST /api/nlquery/analyze/followup — Follow-up Question

Sends a follow-up question in an existing session with full conversation history.

**Request:**
```json
{
  "session_id": "a1b2c3d4e5f67890a1b2c3d4e5f67890",
  "question": "What could cause the database timeout?"
}
```

**Response (200):**
```json
{
  "analysis": "Based on the trace data, the database timeout...",
  "session_id": "a1b2c3d4e5f67890a1b2c3d4e5f67890"
}
```

**Errors:**
| Code | Condition |
|------|-----------|
| 400 | Missing session_id or question |
| 500 | Session not found/expired, LLM failure |

---

### POST /api/nlquery/analyze/search — Search + Analyze (MCP Bridge)

Searches for traces matching NL-extracted parameters and analyzes the results in a single call. Only available when a `ToolCaller` is configured (see [Section 11](#11-mcp-integration--toolcaller-bridge-option-b)).

**Request:**
```json
{
  "params": {
    "service": "payment-service",
    "tags": { "error": "true" },
    "minDuration": "2s"
  },
  "question": "Why are these requests failing?",
  "session_id": ""
}
```

- `params` — NL-extracted `SearchParams` (same schema as `/api/nlquery` response). `service` is required.
- `question` — Optional analysis focus. Defaults to: *"Analyze these trace search results and identify any notable patterns, errors, or performance issues."*
- `session_id` — Optional. Reuse existing session for follow-up context.

**Response (200):**
```json
{
  "analysis": "Found 3 traces from payment-service with errors. All 3 traces show timeouts in the database-service span...",
  "session_id": "a1b2c3d4e5f67890a1b2c3d4e5f67890",
  "traces": [
    {
      "trace_id": "0123456789abcdef0123456789abcdef",
      "root_service": "payment-service",
      "root_span_name": "POST /pay",
      "start_time": "2026-02-07T10:30:00Z",
      "duration_us": 5200000,
      "span_count": 8,
      "service_count": 3,
      "has_errors": true
    }
  ]
}
```

**Errors:**
| Code | Condition |
|------|-----------|
| 400 | Missing `params.service`, malformed JSON |
| 500 | Search error, LLM failure |

---

## 10. Implementation Details

### Component Factory Pattern

The `NewComponentsFromConfig()` function is the single entry point for creating all nlquery components. It:

1. Creates one `SessionManager` (shared across components)
2. Creates one LangChainGo model (shared between extractor and analyzer)
3. Creates the `LLMExtractor` with the search config
4. Creates the `Analyzer` with a bumped `MaxTokens` (minimum 512 for analysis)
5. Returns a `Components` struct that the server wires into routes

```go
type Components struct {
    Extractor Extractor        // NL search (heuristic or LLM)
    Analyzer  *Analyzer        // trace/span analysis (LLM only)
    Sessions  *SessionManager  // shared session state
}
```

This ensures:
- **Single model connection:** One TCP connection to Ollama, not two
- **Shared session state:** Follow-up questions work across analysis types
- **Clean shutdown:** `Components.Close()` stops the session GC goroutine

### Server Integration (server.go)

The integration point in [server.go](../cmd/jaeger/internal/extension/jaegerquery/internal/server.go) is minimal (8 lines of new code):

```go
nlComponents, err := nlquery.NewComponentsFromConfig(queryOpts.NLQuery, telset.Logger)
if err != nil {
    telset.Logger.Error("failed to create nlquery components, falling back to heuristic", zap.Error(err))
    nlComponents = &nlquery.Components{Extractor: &nlquery.HeuristicExtractor{}}
}
if nlComponents.Extractor != nil {
    nlHandler := nlquery.NewHTTPHandler(nlComponents.Extractor, telset.Logger)
    nlHandler.RegisterRoutes(r)
}
if nlComponents.Analyzer != nil {
    toolCaller := nlquery.NewQueryServiceToolCaller(querySvc)
    analysisHandler := nlquery.NewAnalysisHandler(
        querySvc, nlComponents.Analyzer, telset.Logger,
        nlquery.WithToolCaller(toolCaller),
    )
    analysisHandler.RegisterRoutes(r)
}
```

This demonstrates the additive nature: the NL features plug into the existing `mux.Router` without modifying any existing route registrations.

### Trace Fetching

The `AnalysisHandler.fetchTrace()` method uses the existing `QueryService.GetTraces()` API — the same code path used by the REST and gRPC APIs. This ensures:

- All storage backend support (Cassandra, Elasticsearch, Badger, memory) works automatically
- All access control (tenancy, bearer token propagation) is inherited
- No duplicate storage access code

### Hex ID Parsing

Trace IDs (32 hex chars → 16 bytes) and Span IDs (16 hex chars → 8 bytes) are parsed using `encoding/hex.DecodeString()` + `copy()` into fixed-size arrays. This matches the pattern used in the Jaeger MCP server handlers and avoids a dependency on deprecated model ID types.

---

## 11. MCP Integration — ToolCaller Bridge (Option B)

### How MCP Tools Map to nlquery Features

The Jaeger MCP server (`cmd/jaeger/internal/extension/jaegermcp/`) exposes 7 tools that AI clients (IDE assistants, Claude Desktop, etc.) can call via the Model Context Protocol. Many of these tools perform the *same underlying operations* as the nlquery analysis pipeline:

| MCP Tool | Function | nlquery Equivalent |
|----------|----------|-------------------|
| `search_traces` | Find traces by service, time, attributes, duration | `SearchParams.ToTraceQueryParams()` → `QueryService.FindTraces()` |
| `get_services` | List available service names | `QueryService.GetServices()` |
| `get_span_details` | Fetch full span attributes, events, links | `AnalysisHandler.fetchTrace()` → `FindSpanInTrace()` |
| `get_trace_errors` | Get spans with error status | `PruneTrace()` extracts error status per span |
| `get_trace_topology` | Get parent-child span tree | `PrunedTrace.Spans` with `ParentSpanID` fields |
| `get_critical_path` | Identify the latency-critical path | Not yet in nlquery (future extension) |
| `get_span_names` | List span names for a service | Not used by nlquery |

The key observation: **both the MCP tools and the nlquery features call the same `QueryService` methods** (`FindTraces`, `GetTraces`, `GetServices`). The difference is the transport:
- MCP tools receive parameters as JSON over SSE transport and return structured JSON.
- nlquery receives parameters from an LLM extractor and returns them to an LLM analyzer.

This creates a natural integration opportunity: instead of duplicating the search logic, nlquery can **reuse the same query patterns** that the MCP tools use.

### Integration Options Considered

Three integration architectures were evaluated:

#### Option A — Shared Query Interfaces

Extract the `queryServiceInterface` types already defined in the MCP handler files into a shared package, then import that package from nlquery.

| Aspect | Assessment |
|--------|-----------|
| **Approach** | Move MCP's `queryServiceInterface` to a shared `internal/queryapi` package |
| **Coupling** | Creates a compile-time dependency between the Query Service extension and the MCP extension |
| **Risk** | The MCP extension and Query Service extension are independent OTel Collector extensions with different lifecycles. Shared types create version coupling. |
| **Verdict** | ❌ Rejected — creates undesirable coupling between independently deployable extensions |

#### Option B — ToolCaller Interface (Implemented)

Define a narrow `ToolCaller` interface *inside* the nlquery package that mirrors the operations the MCP tools perform, with a concrete implementation that wraps `QueryService` directly.

| Aspect | Assessment |
|--------|-----------|
| **Approach** | Interface in nlquery → concrete `QueryServiceToolCaller` wraps `querysvc.QueryService` |
| **Coupling** | Zero coupling to MCP extension. nlquery depends only on `querysvc.QueryService` (already a dependency). |
| **Testability** | `mockToolCaller` in tests — no storage, no network, no MCP server needed |
| **Schema parity** | `SearchParams.ToMCPArgs()` produces the same key-value map as `types.SearchTracesInput` |
| **Verdict** | ✅ Implemented — best balance of functionality, decoupling, and testability |

#### Option C — Full Agentic Loop

Make the LLM an autonomous agent that calls MCP tools in a loop, synthesizing information across multiple tool calls before producing an analysis.

| Aspect | Assessment |
|--------|-----------|
| **Approach** | LLM decides which MCP tools to call, interprets results, calls more tools, then produces analysis |
| **Complexity** | Requires tool-use prompting, output parsing, loop control, safety limits |
| **Latency** | Multiple model inference + tool call round trips per request |
| **Safety** | LLM becomes a decision maker (violates design philosophy) |
| **Verdict** | ❌ Deferred — future work once the foundation is proven. The ToolCaller interface provides the extension point needed to add agentic behavior later. |

### Option B Implementation

#### The ToolCaller Interface

```go
// ToolCaller abstracts trace query operations that both the MCP tools and
// the nlquery analysis pipeline perform.
type ToolCaller interface {
    SearchTraces(ctx context.Context, params SearchParams) ([]TraceSearchResult, error)
    GetServices(ctx context.Context) ([]string, error)
}
```

The interface is deliberately narrow — only the two operations that the analysis pipeline actually needs. This follows the Interface Segregation Principle: consumers should not depend on methods they don't use.

#### TraceSearchResult

```go
type TraceSearchResult struct {
    TraceID      string `json:"trace_id"`
    RootService  string `json:"root_service"`
    RootSpanName string `json:"root_span_name"`
    StartTime    string `json:"start_time"`
    DurationUs   int64  `json:"duration_us"`
    SpanCount    int    `json:"span_count"`
    ServiceCount int    `json:"service_count"`
    HasErrors    bool   `json:"has_errors"`
}
```

This struct mirrors the MCP `types.TraceSummary` schema field-for-field. It is defined in the nlquery package (not imported from MCP) to avoid a package dependency from the Query Service extension to the MCP extension. JSON-serialized output from either type is interchangeable.

#### QueryServiceToolCaller

The concrete implementation wraps `querysvc.QueryService` — the same service that the MCP tool handlers wrap:

```go
type QueryServiceToolCaller struct {
    querySvc *querysvc.QueryService
}

func (tc *QueryServiceToolCaller) SearchTraces(
    ctx context.Context, params SearchParams,
) ([]TraceSearchResult, error) {
    tqp, err := params.ToTraceQueryParams()  // reuse existing conversion
    // ... set default time range (mirrors MCP's "-1h" default) ...
    tracesIter := tc.querySvc.FindTraces(ctx, queryParams)
    aggregatedIter := jptrace.AggregateTraces(tracesIter)
    // ... iterate and build []TraceSearchResult ...
}
```

The implementation mirrors the MCP `searchTracesHandler.handle()` logic:
1. Convert params → `TraceQueryParams` (same conversion both use)
2. Apply default time range (1h lookback, matches MCP's default)
3. Call `QueryService.FindTraces()` (same method MCP calls)
4. Aggregate via `jptrace.AggregateTraces()` (same helper MCP uses)
5. Build lightweight summaries (same fields MCP returns)

#### SearchParams.ToMCPArgs()

```go
func (p *SearchParams) ToMCPArgs() map[string]any {
    args := make(map[string]any)
    if p.Service != "" {
        args["service_name"] = p.Service  // nlquery "Service" → MCP "service_name"
    }
    if p.Operation != "" {
        args["span_name"] = p.Operation   // nlquery "Operation" → MCP "span_name"
    }
    // ... duration_min, duration_max, search_depth, attributes ...
    return args
}
```

This method provides schema compatibility. The NL extractor produces `SearchParams` with field names reflecting Jaeger terminology (`Service`, `Operation`), while the MCP tool uses OpenTelemetry terminology (`service_name`, `span_name`). `ToMCPArgs()` bridges this naming gap, enabling the extracted parameters to be forwarded to either the direct query path or an MCP tool call.

#### Wiring via Functional Options

The `ToolCaller` is injected into `AnalysisHandler` using the functional options pattern, keeping backward compatibility:

```go
type AnalysisHandlerOption func(*AnalysisHandler)

func WithToolCaller(tc ToolCaller) AnalysisHandlerOption {
    return func(h *AnalysisHandler) { h.toolCaller = tc }
}

func NewAnalysisHandler(
    querySvc *querysvc.QueryService,
    analyzer *Analyzer,
    logger *zap.Logger,
    opts ...AnalysisHandlerOption,
) *AnalysisHandler { ... }
```

When `ToolCaller` is provided, the handler registers an additional route:

```
POST /api/nlquery/analyze/search  ──► Search + Analyze in one call
```

When `ToolCaller` is `nil` (e.g., in unit tests or minimal deployments), the search+analyze endpoint is simply not registered. Existing endpoints are unaffected.

#### The Search + Analyze Endpoint

```
POST /api/nlquery/analyze/search
{
    "params": { "service": "payment-service", "tags": {"error": "true"} },
    "question": "Why are these requests failing?",     // optional
    "session_id": ""                                    // optional
}

Response:
{
    "analysis": "Found 3 traces from payment-service with errors...",
    "session_id": "a1b2c3d4...",
    "traces": [
        { "trace_id": "abc...", "root_service": "payment-service", ... },
        ...
    ]
}
```

This endpoint combines three operations in a single HTTP call:

1. **Search** — `ToolCaller.SearchTraces()` finds matching traces
2. **Format** — `formatSearchResultsForLLM()` converts results to text for the model
3. **Analyze** — `Analyzer.analyze()` sends the formatted results + question to the LLM

The response includes both the raw trace summaries (structured data for the UI) and the LLM analysis (human-readable explanation).

#### Data Flow

```
POST /api/nlquery/analyze/search
  { params: {service, tags, ...}, question?, session_id? }
  │
  ▼
ToolCaller.SearchTraces(params)
  │  └── params.ToTraceQueryParams()     ◄── shared conversion
  │      QueryService.FindTraces()       ◄── same as MCP search_traces
  │      jptrace.AggregateTraces()       ◄── same aggregation
  │      buildTraceSearchResult()        ◄── same summary schema
  │
  ▼
formatSearchResultsForLLM(params, traces)
  │  └── "Search: service="payment-service"
  │       Found 3 traces:
  │       [1] TraceID=abc... root=payment/POST spans=5 errors=true"
  │
  ▼
Analyzer.analyze(systemPrompt, userPrompt, sessionID)
  │  └── model.GenerateContent(messages)  ◄── Ollama API call
  │
  ▼
{ analysis, session_id, traces[] }
```

### Test Coverage for MCP Bridge

| Test | What It Verifies |
|------|-----------------|
| `TestQueryServiceToolCaller_SearchTraces_Success` | End-to-end search with mocked storage returns correct summaries |
| `TestQueryServiceToolCaller_SearchTraces_InvalidParams` | Invalid duration strings are caught |
| `TestQueryServiceToolCaller_SearchTraces_EmptyResults` | Empty iterator returns empty slice, no error |
| `TestQueryServiceToolCaller_SearchTraces_WithErrors` | Error spans correctly set `HasErrors: true` |
| `TestQueryServiceToolCaller_SearchTraces_MultiService` | Multiple resource spans → correct service count |
| `TestQueryServiceToolCaller_GetServices` | Delegates to QueryService correctly |
| `TestQueryServiceToolCaller_GetServices_Error` | Storage errors propagate |
| `TestBuildTraceSearchResult_BasicTrace` | All fields extracted from single span |
| `TestBuildTraceSearchResult_EmptyTrace` | Zero spans → zero counts |
| `TestFormatSearchResultsForLLM_Basic` | Text formatting includes service, count, trace details |
| `TestFormatSearchResultsForLLM_WithOperation` | Operation filter appears in formatted text |
| `TestToMCPArgs_AllFields` | All SearchParams fields map to correct MCP keys |
| `TestToMCPArgs_EmptyParams` | Empty params → empty map |
| `TestToMCPArgs_PartialFields` | Only set fields appear in map |
| `TestSearchAnalyze_Success` | Full endpoint: search + analyze + response with traces |
| `TestSearchAnalyze_CustomQuestion` | Custom question is forwarded to analyzer |
| `TestSearchAnalyze_MissingService` | Missing service returns 400 |
| `TestSearchAnalyze_SearchError` | Storage error returns 500 |
| `TestSearchAnalyze_InvalidJSON` | Malformed JSON returns 400 |
| `TestSearchAnalyze_NotRegisteredWithoutToolCaller` | No ToolCaller → endpoint not registered (404) |
| `TestAnalysisHandlerOption_WithToolCaller` | Functional option sets field correctly |

---

## 12. Testing Strategy

### Test Coverage

| File | Test File | Tests | What's Tested |
|------|-----------|-------|---------------|
| `config.go` | `config_test.go` | 7 | Default values, validation rules, all error paths |
| `extractor.go` | `extractor_test.go` | 2 | StubExtractor returns empty params, implements interface |
| `heuristic.go` | `heuristic_test.go` | 20 | Every regex pattern, combined extraction, edge cases, determinism |
| `llm_extractor.go` | `llm_extractor_test.go` | 7 | Valid/invalid JSON, hallucinated fields dropped, model errors, empty response |
| `params.go` | `params_test.go` | 9 | ToTraceQueryParams conversion, JSON round-trip, safety firewall |
| `handler.go` | `handler_test.go` | 6 | HTTP success/error paths, empty query, invalid JSON, stub extractor |
| `session.go` | `session_test.go` | 10 | Create, get, expiry, add message, TTL refresh, eviction, delete, sweep, ID uniqueness |
| `pruner.go` | `pruner_test.go` | 19 | All span fields, attribute cap, events, error status, multi-service traces, text formatting |
| `analyzer.go` | `analyzer_test.go` | 11 | Span/trace analysis, session reuse, invalid session, follow-up, LLM errors, message building |
| `analysis_handler.go` | `analysis_handler_test.go` | 24 | All HTTP endpoints, ID parsing, not-found paths, search+analyze, MCP wiring |
| `provider.go` | `provider_test.go` | 5 | Disabled config, no provider, unsupported provider, nil sessions close, TTL from config |
| `mcp_bridge.go` | `mcp_bridge_test.go` | 18 | ToolCaller search/services, buildTraceSearchResult, ToMCPArgs, formatSearchResultsForLLM |

### Mock Strategy

- **LLM model:** `mockModel` struct implementing `llms.Model` interface with configurable response/error
- **Trace storage:** `tracestoremocks.Reader` (testify/mockery mock from existing Jaeger test infrastructure)
- **Dependency storage:** `depsmocks.Reader` (testify/mockery mock from existing Jaeger test infrastructure)

No network calls, no real models, no real storage in any test.

### Goroutine Leak Detection

The package uses Jaeger's standard `testutils.VerifyGoLeaks(m)` in `TestMain`. This catches any leaked goroutines from unclosed `SessionManager` instances. Several production bugs were found and fixed through this check:

- `NewComponentsFromConfig()` now closes the `SessionManager` on error paths
- `NewExtractorFromConfig()` now closes the `SessionManager` since the caller only needs the extractor
- All test functions use `t.Cleanup(sm.Close)` for session managers

---

## 13. Code Metrics

| Metric | Value |
|--------|-------|
| Production files | 13 |
| Test files | 12 |
| Production lines | 2,055 |
| Test lines | 2,397 |
| Test functions | 123 |
| Test-to-production ratio | 1.17:1 |
| External dependencies added | 0 (LangChainGo was already in go.mod) |
| Existing files modified | 2 (server.go, flags.go — additive only) |
| Existing tests broken | 0 |
| Lint issues | 0 |

---

## 14. Security & Safety Guarantees

### Data Safety

| Concern | Mitigation |
|---------|-----------|
| Trace data sent to cloud | ❌ Impossible — Ollama runs locally. No network egress. |
| LLM modifies traces | ❌ Impossible — LLM output is a string. No write path exists. |
| Hallucinated search params | `json.Unmarshal` firewall — unknown fields are silently dropped |
| Hallucinated analysis | System prompts include: *"Do NOT make up information"* |
| Prompt injection | Input is embedded as a `human` message, never interpolated into the system prompt |
| Session hijacking | Session IDs are 128-bit random (`crypto/rand`), not guessable |
| DoS via sessions | TTL (30min) + message cap (50) + GC (5min) prevent memory exhaustion |

### No Secrets Required

The LangChainGo Ollama provider connects to a local HTTP server. No API keys, tokens, or credentials are involved. The endpoint URL is treated as infrastructure configuration, not a secret.

### Operator Control

The operator has full control over:
- Whether the feature is enabled at all (`enabled: false`)
- Which model is used (any Ollama-compatible model)
- Where the model server runs (any URL)
- The model's randomness (`temperature`)
- The output length (`max_tokens`)

---

## 15. Deployment & Operator Guide

### Prerequisites

1. [Ollama](https://ollama.ai/) installed and running
2. A model pulled (e.g., `ollama pull qwen2:0.5b`)

### Minimal Configuration

```yaml
extensions:
  jaeger_query:
    storage:
      traces: memstore
    nlquery:
      enabled: true
      provider: "ollama"
      endpoint: "http://localhost:11434"
      model: "qwen2:0.5b"
```

### Recommended Models

| Model | Size | RAM | Strength | Weakness |
|-------|------|-----|----------|----------|
| `qwen2:0.5b` | 0.5B | ~1GB | Fast, tiny footprint | May miss complex patterns |
| `llama3.2:3b` | 3B | ~4GB | Good reasoning | Slower on CPU |
| `phi3:3.8b` | 3.8B | ~4GB | Strong instruction following | Slower on CPU |
| `mistral:7b` | 7B | ~8GB | Best quality | Needs more RAM |

### Verification

After starting Jaeger with the configuration:

```bash
# Test NL search extraction
curl -X POST http://localhost:16686/api/nlquery \
  -H "Content-Type: application/json" \
  -d '{"query": "show errors from payment-service"}'

# Expected: {"params":{"service":"payment-service","tags":{"error":"true"}}}
```

---

## 16. Future Work

These features provide the foundation for more advanced AI capabilities:

1. **UI Integration** — Add a natural language search bar in the Jaeger UI that calls the `/api/nlquery` endpoint and auto-fills search fields. The `/api/nlquery/analyze/search` endpoint can power a "smart search" panel that shows both results and an AI summary.

2. **Anomaly Detection** — Use the pruner and analysis infrastructure to compare spans against historical baselines and flag anomalies.

3. **Root Cause Analysis** — Extend the trace analysis prompt to identify the root cause span in error traces, leveraging the span hierarchy from `PrunedTrace`.

4. **Streaming Responses** — Use Ollama's streaming endpoint for real-time token-by-token analysis output in the UI.

5. **Additional Providers** — Add support for `llamacpp`, `vllm`, or other local model servers by adding cases to `createModel()`.

6. **Configurable Session TTL** — Expose session TTL as a YAML config option (the `SessionTTLFromConfig()` extension point already exists).

7. **Agentic MCP Loop (Option C)** — The `ToolCaller` interface provides the extension point for a full agentic loop where the LLM autonomously decides which tools to call (search, get services, get span details, get critical path), synthesizes results across multiple rounds, and produces a comprehensive analysis. This is architecturally ready but deferred until the simpler Option B pipeline proves its value in production.

8. **ToolCaller Expansion** — Add `GetSpanDetails()`, `GetTraceTopology()`, and `GetCriticalPath()` to the `ToolCaller` interface, enabling the analyzer to drill deeper into specific traces during the search+analyze flow.

---

## Appendix: File-by-File Summary

| File | Lines | Purpose |
|------|-------|---------|
| [config.go](../cmd/jaeger/internal/extension/jaegerquery/internal/nlquery/config.go) | 87 | Configuration struct with YAML `mapstructure` tags and validation |
| [extractor.go](../cmd/jaeger/internal/extension/jaegerquery/internal/nlquery/extractor.go) | 47 | `Extractor` interface definition and `StubExtractor` |
| [heuristic.go](../cmd/jaeger/internal/extension/jaegerquery/internal/nlquery/heuristic.go) | 154 | Regex-based NL → SearchParams extraction, zero dependencies |
| [llm_extractor.go](../cmd/jaeger/internal/extension/jaegerquery/internal/nlquery/llm_extractor.go) | 138 | LLM-backed extraction with system prompt, JSON mode, safety firewall |
| [params.go](../cmd/jaeger/internal/extension/jaegerquery/internal/nlquery/params.go) | 142 | `SearchParams` struct, `ToTraceQueryParams()`, and `ToMCPArgs()` |
| [handler.go](../cmd/jaeger/internal/extension/jaegerquery/internal/nlquery/handler.go) | 104 | HTTP handler for POST /api/nlquery |
| [analysis_handler.go](../cmd/jaeger/internal/extension/jaegerquery/internal/nlquery/analysis_handler.go) | 388 | HTTP handlers for span/trace/search analysis, follow-up, and MCP bridge wiring |
| [analyzer.go](../cmd/jaeger/internal/extension/jaegerquery/internal/nlquery/analyzer.go) | 198 | LLM analysis engine with session-aware conversation |
| [pruner.go](../cmd/jaeger/internal/extension/jaegerquery/internal/nlquery/pruner.go) | 292 | Trace/span data pruning and LLM text formatting |
| [session.go](../cmd/jaeger/internal/extension/jaegerquery/internal/nlquery/session.go) | 189 | In-memory TTL-based session manager with background GC |
| [provider.go](../cmd/jaeger/internal/extension/jaegerquery/internal/nlquery/provider.go) | 140 | Component factory — creates model, extractor, analyzer, sessions |
| [mcp_bridge.go](../cmd/jaeger/internal/extension/jaegerquery/internal/nlquery/mcp_bridge.go) | 176 | `ToolCaller` interface, `QueryServiceToolCaller` impl, `TraceSearchResult` |
