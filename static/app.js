const peerIdInput = document.getElementById("peerId");
const messageInput = document.getElementById("message");
const sendBtn = document.getElementById("sendBtn");
const simulateBtn = document.getElementById("simulateBtn");
const clearBtn = document.getElementById("clearBtn");
const logList = document.getElementById("log");
const counter = document.getElementById("counter");

function timeStamp() {
  return new Date().toLocaleTimeString();
}

function escapeHtml(text) {
  return text
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function addLog(kind, peer, text) {
  const li = document.createElement("li");
  li.className = kind === "send" ? "kind-send" : "kind-receive";

  li.innerHTML = `
    <span class="meta">${kind === "send" ? "Sent" : "Received"} • ${escapeHtml(peer)} • ${timeStamp()}</span>
    <strong>${escapeHtml(text)}</strong>
  `;

  logList.prepend(li);
  counter.textContent = `${logList.children.length} events`;
}

function readForm() {
  const peerId = peerIdInput.value.trim() || "unknown-peer";
  const content = messageInput.value.trim();
  return { peerId, content };
}

sendBtn.addEventListener("click", () => {
  const { peerId, content } = readForm();
  if (!content) return;

  // Test-only behavior: log outgoing event. Replace with real API call later.
  addLog("send", peerId, content);
  messageInput.value = "";
  messageInput.focus();
});

simulateBtn.addEventListener("click", () => {
  const peerId = peerIdInput.value.trim() || "peer-1";
  addLog("receive", peerId, "Pong from local mock peer");
});

clearBtn.addEventListener("click", () => {
  logList.innerHTML = "";
  counter.textContent = "0 events";
});

messageInput.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
    sendBtn.click();
  }
});

addLog("receive", "system", "UI ready. Use Send or Simulate Incoming.");
