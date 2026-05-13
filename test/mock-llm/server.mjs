#!/usr/bin/env node
/**
 * mock-llm — simulates SecretAI / Ollama LLM API responses
 * Implements the subset used by secretai-compose (Ollama-compatible OpenAI API):
 *   POST /v1/chat/completions  → OpenAI chat completion (non-streaming)
 *   POST /v1/embeddings        → embedding response
 *   GET  /v1/models            → model list (no tokens)
 *   GET  /                     → Ollama health ("Ollama is running")
 *   GET  /health               → 200 OK
 *   GET  /api/tags             → Ollama model list format
 */

import http from 'http';

const PORT = 80;

const MODELS = [
    { id: 'llama-3.3-70b-instruct', object: 'model', created: 1700000000, owned_by: 'secretai' },
    { id: 'deepseek-r1:70b', object: 'model', created: 1700000000, owned_by: 'secretai' },
    { id: 'gemma3:4b', object: 'model', created: 1700000000, owned_by: 'secretai' },
    { id: 'qwen3:8b', object: 'model', created: 1700000000, owned_by: 'secretai' },
];

const OLLAMA_TAGS = {
    models: MODELS.map(m => ({
        name: m.id,
        model: m.id,
        modified_at: '2024-01-01T00:00:00Z',
        size: 70_000_000_000,
        digest: 'sha256:mock',
        details: { family: 'llama', parameter_size: '70B', quantization_level: 'Q4_0' },
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

function chatCompletion(modelName) {
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

const server = http.createServer(async (req, res) => {
    const url = new URL(req.url, `http://localhost:${PORT}`);
    const { pathname } = url;

    // ── Health / Ollama root ──────────────────────────────────────────────────
    if (req.method === 'GET' && (pathname === '/' || pathname === '/health')) {
        res.writeHead(200, { 'Content-Type': 'text/plain' });
        res.end('Ollama is running');
        return;
    }

    // ── GET /v1/models ────────────────────────────────────────────────────────
    if (req.method === 'GET' && pathname === '/v1/models') {
        json(res, 200, { object: 'list', data: MODELS });
        return;
    }

    // ── GET /api/tags (Ollama native format) ──────────────────────────────────
    if (req.method === 'GET' && pathname === '/api/tags') {
        json(res, 200, OLLAMA_TAGS);
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
        json(res, 200, chatCompletion(modelName));
        return;
    }

    // ── POST /api/chat (Ollama native format) ─────────────────────────────────
    if (req.method === 'POST' && pathname === '/api/chat') {
        const body = await readBody(req);
        let modelName = 'llama-3.3-70b-instruct';
        try {
            const parsed = JSON.parse(body);
            if (parsed.model) modelName = parsed.model;
        } catch { }
        // Ollama non-streaming response
        json(res, 200, {
            model: modelName,
            created_at: new Date().toISOString(),
            message: { role: 'assistant', content: `Mock response from ${modelName}` },
            done: true,
            total_duration: 1_000_000_000,
            prompt_eval_count: 25,
            eval_count: 28,
        });
        return;
    }

    // ── POST /v1/embeddings ───────────────────────────────────────────────────
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
    console.log(`Endpoints:`);
    console.log(`  GET  /          → Ollama health`);
    console.log(`  GET  /v1/models → model list (no billing)`);
    console.log(`  GET  /api/tags  → Ollama tags`);
    console.log(`  POST /v1/chat/completions → chat completion (usage: 25+28 tokens)`);
    console.log(`  POST /api/chat            → Ollama chat`);
    console.log(`  POST /v1/embeddings       → embeddings`);
});
