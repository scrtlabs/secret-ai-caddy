#!/usr/bin/env node
// Signs an x402 agent request (EIP-191 personal_sign)
// Usage: node local/sign_request.js <privateKey> <METHOD> <path> <body> [timestamp]
// Example: node local/sign_request.js 0xabc... POST /v1/chat/completions '{"model":"gpt-4"}'
import { ethers } from "/opt/homebrew/lib/node_modules/secretvm-verify/node_modules/ethers/lib.esm/index.js";
import { createHash } from "crypto";

const [, , privateKey, method, path, body = "", tsArg] = process.argv;

if (!privateKey || !method || !path) {
    console.error("Usage: node sign_request.js <privateKey> <METHOD> <path> [body] [timestamp]");
    process.exit(1);
}

const timestamp = tsArg || String(Math.floor(Date.now() / 1000));
const payload = method + path + body + timestamp;
const hashBytes = createHash("sha256").update(payload).digest();

const wallet = new ethers.Wallet(privateKey);
// Path 1: sign the raw 32 hash bytes (matching DevPortal verifySignature.ts)
const sig = await wallet.signMessage(hashBytes);

console.log(JSON.stringify({
    wallet: wallet.address,
    timestamp,
    signature: sig,
    method,
    path,
    body,
}));
