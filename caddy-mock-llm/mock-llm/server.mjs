#!/usr/bin/env node
/**
 * mock-llm — simulates SecretAI / Ollama LLM API responses
 *
 * Ollama-native endpoints (used by SecretAI compose):
 *   GET  /                       → health ("Ollama is running")
 *   GET  /api/version            → { version: "0.12.3" }
 *   GET  /api/tags               → model list (Ollama format)
 *   POST /api/chat               → Ollama chat (non-streaming), supports think=true
 *   POST /api/generate           → Ollama generate (non-streaming)
 *   POST /api/show               → model info
 *   POST /api/embed              → embeddings (Ollama v2 format)
 *
 * OpenAI-compatible endpoints (proxied via Caddy):
 *   GET  /v1/models              → model list (OpenAI format)
 *   POST /v1/chat/completions    → chat completion (OpenAI format, usage: 25+28 tokens)
 *   POST /v1/embeddings          → embeddings (OpenAI format)
 */

import http from 'http';

const PORT = 80;

const MODEL_DEFS = [
    { name: 'llama-3.3-70b-instruct', family: 'llama',     size: '70B' },
    { name: 'deepseek-r1:70b',        family: 'deepseek',  size: '70B' },
    { name: 'gemma3:4b',              family: 'gemma',     size: '4B'  },
    { name: 'qwen3:8b',               family: 'qwen',      size: '8B'  },
];

// OpenAI-style model list
const OAI_MODELS = MODEL_DEFS.map(m => ({
    id: m.name,
    object: 'model',
    created: 1700000000,
    owned_by: 'secretai',
}));

// Ollama /api/tags format
const OLLAMA_TAGS = {
    models: MODEL_DEFS.map(m => ({
        name: m.name,
        model: m.name,
        modified_at: '2025-01-01T00:00:00Z',
        size: 70_000_000_000,
        digest: 'sha256:mock0000000000000000000000000000000000000000000000000000000000000000',
        details: {
            parent_model: '',
            format: 'gguf',
            family: m.family,
            families: [m.family],
            parameter_size: m.size,
            quantization_level: 'Q4_K_M',
        },
    })),
};

function readBody(req) {
    return new Promise((resolve) => {
        let body = '';
        req.on('data', chunk => (body += chunk));
        req.on('end', () => resolve(body));
    });
}

function json(res, status, data) {
    const payload = JSON.stringify(data);
    res.writeHead(status, {
        'Content-Type': 'application/json; charset=utf-8',
        'Content-Length': Buffer.byteLength(payload),
    });
    res.end(payload);
}

// ── Ollama /api/chat response builder ─────────────────────────────────────────
function ollamaChat(modelName, think) {
    const content = `Mock response from ${modelName}. This simulates a real LLM reply for billing pipeline testing.`;
    const msg = { role: 'assistant', content };
    if (think) {
        msg.thinking = `Mock thinking trace: considering how to answer this query using ${modelName}.`;
    }
    return {
        model: modelName,
        created_at: new Date().toISOString(),
        message: msg,
        done_reason: 'stop',
        done: true,
        total_duration:       1_234_567_890,
        load_duration:          123_456_789,
        prompt_eval_count:    25,
        prompt_eval_duration: 250_000_000,
        eval_count:           28,
        eval_duration:        950_000_000,
    };
}

// ── Ollama /api/generate response builder ────────────────────────────────────
function ollamaGenerate(modelName, think) {
    const response = `Mock generated text from ${modelName}.`;
    const result = {
        model: modelName,
        created_at: new Date().toISOString(),
        response,
        done_reason: 'stop',
        done: true,
        total_duration:       1_234_567_890,
        load_duration:          123_456_789,
        prompt_eval_count:    25,
        prompt_eval_duration: 250_000_000,
        eval_count:           28,
        eval_duration:        950_000_000,
    };
    if (think) {
        result.thinking = `Mock thinking trace from ${modelName}.`;
    }
    return result;
}

// ── OpenAI /v1/chat/completions response builder ────────────────────────────
function oaiChat(modelName) {
    const content = `Hello! I am a mock SecretAI LLM (${modelName}). ` +
        'This response simulates a real LLM reply to verify billing and token reporting works correctly.';
    return {
        id: `chatcmpl-mock-${Date.now()}`,
        object: 'chat.completion',
        created: Math.floor(Date.now() / 1000),
        model: modelName,
        choices: [{
            index: 0,
            message: { role: 'assistant', content },
            finish_reason: 'stop',
        }],
        usage: {
            prompt_tokens: 25,
            completion_tokens: 28,
            total_tokens: 53,
        },
    };
}

// ── Request router ────────────────────────────────────────────────────────────
const server = http.createServer(async (req, res) => {
    const url = new URL(req.url, `http://localhost:${PORT}`);
    const { pathname } = url;

    // ── Health ────────────────────────────────────────────────────────────────
    if (req.method === 'GET' && (pathname === '/' || pathname === '/health')) {
        res.writeHead(200, { 'Content-Type': 'text/plain' });
        res.end('Ollama is running');
        return;
    }

    // ── GET /api/version ──────────────────────────────────────────────────────
    if (req.method === 'GET' && pathname === '/api/version') {
        json(res, 200, { version: '0.12.3' });
        return;
    }

    // ── GET /api/tags ─────────────────────────────────────────────────────────
    if (req.method === 'GET' && pathname === '/api/tags') {
        json(res, 200, OLLAMA_TAGS);
        return;
    }

    // ── GET /v1/models ────────────────────────────────────────────────────────
    if (req.method === 'GET' && pathname === '/v1/models') {
        json(res, 200, { object: 'list', data: OAI_MODELS });
        return;
    }

    // ── POST /api/chat ────────────────────────────────────────────────────────
    if (req.method === 'POST' && pathname === '/api/chat') {
        const body = await readBody(req);
        let modelName = 'llama-3.3-70b-instruct';
        let think = false;
        try {
            const parsed = JSON.parse(body);
            if (parsed.model) modelName = parsed.model;
            if (parsed.think) think = true;
        } catch { }
        json(res, 200, ollamaChat(modelName, think));
        return;
    }

    // ── POST /api/generate ────────────────────────────────────────────────────
    if (req.method === 'POST' && pathname === '/api/generate') {
        const body = await readBody(req);
        let modelName = 'llama-3.3-70b-instruct';
        let think = false;
        try {
            const parsed = JSON.parse(body);
            if (parsed.model) modelName = parsed.model;
            if (parsed.think) think = true;
        } catch { }
        json(res, 200, ollamaGenerate(modelName, think));
        return;
    }

    // ── POST /api/show ────────────────────────────────────────────────────────
    if (req.method === 'POST' && pathname === '/api/show') {
        const body = await readBody(req);
        let modelName = 'llama-3.3-70b-instruct';
        try {
            const parsed = JSON.parse(body);
            if (parsed.model) modelName = parsed.model;
        } catch { }
        const def = MODEL_DEFS.find(m => m.name === modelName) || MODEL_DEFS[0];
        json(res, 200, {
            modelfile: `FROM ${modelName}\n`,
            parameters: 'num_ctx 4096\ntemperature 0.7',
            template: '{{ .System }}\n{{ .Prompt }}',
            details: {
                parent_model: '',
                format: 'gguf',
                family: def.family,
                families: [def.family],
                parameter_size: def.size,
                quantization_level: 'Q4_K_M',
            },
            model_info: {
                'general.architecture': def.family,
                'general.parameter_count': 70_000_000_000,
            },
        });
        return;
    }

    // ── POST /api/embed (Ollama v2) ───────────────────────────────────────────
    if (req.method === 'POST' && pathname === '/api/embed') {
        const body = await readBody(req);
        let modelName = 'llama-3.3-70b-instruct';
        try {
            const parsed = JSON.parse(body);
            if (parsed.model) modelName = parsed.model;
        } catch { }
        json(res, 200, {
            model: modelName,
            embeddings: [Array.from({ length: 8 }, (_, i) => (i + 1) * 0.1)],
            total_duration: 100_000_000,
            load_duration:   10_000_000,
            prompt_eval_count: 5,
        });
        return;
    }

    // ── POST /v1/chat/completions ─────────────────────────────────────────────
    if (req.method === 'POST' && pathname === '/v1/chat/completions') {
        const body = await readBody(req);
        let modelName = 'llama-3.3-70b-instruct';
        try {
            const parsed = JSON.parse(body);
            if (parsed.model) modelName = parsed.model;
        } catch { }
        json(res, 200, oaiChat(modelName));
        return;
    }

    // ── POST /v1/embeddings (OpenAI format) ───────────────────────────────────
    if (req.method === 'POST' && pathname === '/v1/embeddings') {
        const body = await readBody(req);
        let modelName = 'text-embedding-ada-002';
        try {
            const parsed = JSON.parse(body);
            if (parsed.model) modelName = parsed.model;
        } catch { }
        json(res, 200, {
            object: 'list',
            data: [{ object: 'embedding', embedding: Array.from({ length: 8 }, (_, i) => (i + 1) * 0.1), index: 0 }],
            model: modelName,
            usage: { prompt_tokens: 5, total_tokens: 5 },
        });
        return;
    }

    // ── Fallback ──────────────────────────────────────────────────────────────
    json(res, 404, { error: 'Not found', path: pathname, method: req.method });
});

server.listen(PORT, () => {
    console.log(`mock-llm listening on :${PORT}`);
    console.log('Endpoints:');
    console.log('  GET  /               → Ollama health');
    console.log('  GET  /api/version    → version info');
    console.log('  GET  /api/tags       → Ollama model list');
    console.log('  POST /api/chat       → Ollama chat (think supported)');
    console.log('  POST /api/generate   → Ollama generate (think supported)');
    console.log('  POST /api/show       → model info');
    console.log('  POST /api/embed      → Ollama v2 embeddings');
    console.log('  GET  /v1/models      → OpenAI model list');
    console.log('  POST /v1/chat/completions → OpenAI chat (usage: 25+28 tokens)');
    console.log('  POST /v1/embeddings  → OpenAI embeddings');
});
