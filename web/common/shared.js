const API_BASE = "http://localhost:8080";
const AUTH_KEY = "pim_auth_session";
const TRACE_HISTORY_KEY = "pim_trace_history";

// 保存当前会话认证信息。使用 sessionStorage 以隔离不同浏览器标签页登录态。
function saveAuth(token, user) {
  sessionStorage.setItem(AUTH_KEY, JSON.stringify({ token, user }));
}

// 读取认证信息。解析异常时按未登录处理，避免页面初始化中断。
function loadAuth() {
  try {
    const raw = sessionStorage.getItem(AUTH_KEY);
    if (!raw) return { token: "", user: null };
    const data = JSON.parse(raw);
    return {
      token: data?.token || "",
      user: data?.user || null,
    };
  } catch (_) {
    return { token: "", user: null };
  }
}

function clearAuth() {
  sessionStorage.removeItem(AUTH_KEY);
}

function saveTraceID(traceID) {
  const id = String(traceID || "").trim();
  if (!id) return;
  try {
    const raw = sessionStorage.getItem(TRACE_HISTORY_KEY);
    const list = raw ? JSON.parse(raw) : [];
    const next = [id, ...list.filter((x) => x !== id)].slice(0, 30);
    sessionStorage.setItem(TRACE_HISTORY_KEY, JSON.stringify(next));
  } catch (_) {}
}

function loadTraceHistory() {
  try {
    const raw = sessionStorage.getItem(TRACE_HISTORY_KEY);
    const list = raw ? JSON.parse(raw) : [];
    return Array.isArray(list) ? list : [];
  } catch (_) {
    return [];
  }
}

// 网关 API 统一请求方法：自动附带 token、统一解析错误消息。
async function apiRequest(path, options = {}) {
  const { token } = loadAuth();
  const headers = { "Content-Type": "application/json", ...(options.headers || {}) };
  if (token) headers.Authorization = `Bearer ${token}`;

  const resp = await fetch(API_BASE + path, {
    method: options.method || "GET",
    headers,
    body: options.body ? JSON.stringify(options.body) : undefined,
  });
  saveTraceID(resp.headers.get("X-Trace-Id") || "");
  const text = await resp.text();
  let data = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch (_) {
    data = null;
  }
  if (!resp.ok) throw new Error(data?.error || `HTTP ${resp.status}`);
  return data;
}
