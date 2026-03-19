'use strict';

const {
  toStringSafe,
} = require('./state');
const {
  buildToolCallCandidates,
  parseToolCallsPayload,
  parseMarkupToolCalls,
  parseTextKVToolCalls,
} = require('./parse_payload');

const TOOL_NAME_LOOSE_PATTERN = /[^a-z0-9]+/g;

function extractToolNames(tools) {
  if (!Array.isArray(tools) || tools.length === 0) {
    return [];
  }
  const out = [];
  for (const t of tools) {
    if (!t || typeof t !== 'object') {
      continue;
    }
    const fn = t.function && typeof t.function === 'object' ? t.function : t;
    const name = toStringSafe(fn.name);
    // Keep parity with Go injectToolPrompt: object tools without name still
    // enter tool mode via fallback name "unknown".
    out.push(name || 'unknown');
  }
  return out;
}

function parseToolCalls(text, toolNames) {
  return parseToolCallsDetailed(text, toolNames).calls;
}

function parseToolCallsDetailed(text, toolNames) {
  const result = emptyParseResult();
  const normalized = toStringSafe(text);
  if (!normalized) {
    return result;
  }
  result.sawToolCallSyntax = looksLikeToolCallSyntax(normalized);

  const candidates = buildToolCallCandidates(normalized);
  let parsed = [];
  for (const c of candidates) {
    parsed = parseToolCallsPayload(c);
    if (parsed.length === 0) {
      parsed = parseMarkupToolCalls(c);
    }
    if (parsed.length === 0) {
      parsed = parseTextKVToolCalls(c);
    }
    if (parsed.length > 0) {
      result.sawToolCallSyntax = true;
      break;
    }
  }
  if (parsed.length === 0) {
    parsed = parseMarkupToolCalls(normalized);
    if (parsed.length === 0) {
      parsed = parseTextKVToolCalls(normalized);
      if (parsed.length === 0) {
        return result;
      }
    }
    result.sawToolCallSyntax = true;
  }

  const filtered = filterToolCallsDetailed(parsed, toolNames);
  result.calls = filtered.calls;
  result.rejectedToolNames = filtered.rejectedToolNames;
  result.rejectedByPolicy = filtered.rejectedToolNames.length > 0 && filtered.calls.length === 0;
  return result;
}

function parseStandaloneToolCalls(text, toolNames) {
  return parseStandaloneToolCallsDetailed(text, toolNames).calls;
}

function parseStandaloneToolCallsDetailed(text, toolNames) {
  const result = emptyParseResult();
  const trimmed = toStringSafe(text);
  if (!trimmed) {
    return result;
  }
  result.sawToolCallSyntax = looksLikeToolCallSyntax(trimmed);
  const candidates = buildToolCallCandidates(trimmed);
  let parsed = [];
  for (const c of candidates) {
    parsed = parseToolCallsPayload(c);
    if (parsed.length === 0) {
      parsed = parseMarkupToolCalls(c);
    }
    if (parsed.length === 0) {
      parsed = parseTextKVToolCalls(c);
    }
    if (parsed.length > 0) {
      break;
    }
  }
  if (parsed.length === 0) {
    parsed = parseMarkupToolCalls(trimmed);
    if (parsed.length === 0) {
      parsed = parseTextKVToolCalls(trimmed);
      if (parsed.length === 0) {
        return result;
      }
    }
  }

  result.sawToolCallSyntax = true;
  const filtered = filterToolCallsDetailed(parsed, toolNames);
  result.calls = filtered.calls;
  result.rejectedToolNames = filtered.rejectedToolNames;
  result.rejectedByPolicy = filtered.rejectedToolNames.length > 0 && filtered.calls.length === 0;
  return result;
}

function emptyParseResult() {
  return {
    calls: [],
    sawToolCallSyntax: false,
    rejectedByPolicy: false,
    rejectedToolNames: [],
  };
}

function filterToolCallsDetailed(parsed, toolNames) {
  const sourceNames = Array.isArray(toolNames) ? toolNames : [];
  const allowed = new Set();
  const allowedCanonical = new Map();
  for (const item of sourceNames) {
    const name = toStringSafe(item);
    if (!name) {
      continue;
    }
    allowed.add(name);
    const lower = name.toLowerCase();
    if (!allowedCanonical.has(lower)) {
      allowedCanonical.set(lower, name);
    }
  }

  if (allowed.size === 0) {
    const rejected = [];
    const seen = new Set();
    for (const tc of parsed) {
      if (!tc || !tc.name) {
        continue;
      }
      if (seen.has(tc.name)) {
        continue;
      }
      seen.add(tc.name);
      rejected.push(tc.name);
    }
    return { calls: [], rejectedToolNames: rejected };
  }

  const calls = [];
  const rejected = [];
  const seenRejected = new Set();
  for (const tc of parsed) {
    if (!tc || !tc.name) {
      continue;
    }
    let matchedName = '';
    if (allowed.has(tc.name)) {
      matchedName = tc.name;
    } else {
      matchedName = resolveAllowedToolName(tc.name, allowed, allowedCanonical);
    }
    if (!matchedName) {
      if (!seenRejected.has(tc.name)) {
        seenRejected.add(tc.name);
        rejected.push(tc.name);
      }
      continue;
    }
    calls.push({
      name: matchedName,
      input: tc.input && typeof tc.input === 'object' && !Array.isArray(tc.input) ? tc.input : {},
    });
  }
  return { calls, rejectedToolNames: rejected };
}

function resolveAllowedToolName(name, allowed, allowedCanonical) {
  const normalizedName = toStringSafe(name).trim();
  if (!normalizedName) {
    return '';
  }
  if (allowed.has(normalizedName)) {
    return normalizedName;
  }
  const lower = normalizedName.toLowerCase();
  if (allowedCanonical.has(lower)) {
    return allowedCanonical.get(lower);
  }
  const idx = lower.lastIndexOf('.');
  if (idx >= 0 && idx < lower.length - 1) {
    const tail = lower.slice(idx + 1);
    if (allowedCanonical.has(tail)) {
      return allowedCanonical.get(tail);
    }
  }
  const loose = lower.replace(TOOL_NAME_LOOSE_PATTERN, '');
  if (!loose) {
    return '';
  }
  for (const [candidateLower, canonical] of allowedCanonical.entries()) {
    if (candidateLower.replace(TOOL_NAME_LOOSE_PATTERN, '') === loose) {
      return canonical;
    }
  }
  return '';
}

function looksLikeToolCallSyntax(text) {
  const lower = toStringSafe(text).toLowerCase();
  return lower.includes('tool_calls')
    || lower.includes('<tool_call')
    || lower.includes('<function_call')
    || lower.includes('<invoke')
    || lower.includes('function.name:');
}

module.exports = {
  extractToolNames,
  parseToolCalls,
  parseToolCallsDetailed,
  parseStandaloneToolCalls,
  parseStandaloneToolCallsDetailed,
};
