import type {
  AppEvent,
  BlockedPeer,
  ChatSummary,
  Contact,
  InviteCode,
  Profile,
  Snapshot,
  UIMessage,
} from "../types";

const API_BASE =
  import.meta.env.VITE_API_BASE?.toString() ?? "http://127.0.0.1:38673";

export function getApiBase() {
  return API_BASE;
}

function buildUrl(path: string) {
  try {
    return new URL(path, API_BASE).toString();
  } catch {
    throw new Error("Bridge address is invalid. Check the local backend.");
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(buildUrl(path), {
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
    ...init,
  });

  if (!response.ok) {
    const fallback = `Request failed: ${response.status}`;
    try {
      const body = (await response.json()) as { error?: string };
      throw new Error(body.error ?? fallback);
    } catch (error) {
      if (error instanceof Error) {
        throw error;
      }
      throw new Error(fallback);
    }
  }

  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export function loadBootstrap() {
  return request<Snapshot>("/api/bootstrap");
}

export function setPeerId(peerId: string) {
  return request<Profile>("/api/profile", {
    method: "PATCH",
    body: JSON.stringify({ peer_id: peerId }),
  });
}

export function loadMessages(chatId: string) {
  return request<UIMessage[]>(
    `/api/chats/${encodeURIComponent(chatId)}/messages`,
  );
}

export function openPrivateChat(payload: {
  peer_id: string;
  peer_addr?: string;
  name?: string;
}) {
  return request<ChatSummary>("/api/chats/open", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export function markChatRead(chatId: string) {
  return request<{ ok: true }>("/api/chats/read", {
    method: "POST",
    body: JSON.stringify({ chat_id: chatId }),
  });
}

export function sendMessage(payload: {
  chat_id: string;
  target_id: string;
  text: string;
}) {
  return request<UIMessage>("/api/messages", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export function loadInviteCode() {
  return request<InviteCode>("/api/invite");
}

export function resolveInviteCode(code: string) {
  return request<{ peer_id: string }>("/api/invite/resolve", {
    method: "POST",
    body: JSON.stringify({ code }),
  });
}

export function saveContact(contact: Contact) {
  return request<Contact>("/api/contacts", {
    method: "POST",
    body: JSON.stringify(contact),
  });
}

export function renameContact(query: string, name: string) {
  return request<Contact>(`/api/contacts/${encodeURIComponent(query)}`, {
    method: "PATCH",
    body: JSON.stringify({ name }),
  });
}

export function deleteContact(query: string) {
  return request<{ ok: true }>(`/api/contacts/${encodeURIComponent(query)}`, {
    method: "DELETE",
  });
}

export function blockPeer(payload: { query: string; reason?: string }) {
  return request<BlockedPeer>("/api/blocked", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export function unblockPeer(query: string) {
  return request<{ ok: true }>(`/api/blocked/${encodeURIComponent(query)}`, {
    method: "DELETE",
  });
}

export function listenEvents(onEvent: (event: AppEvent) => void) {
  const source = new EventSource(buildUrl("/api/events"));

  const forward = (nativeEvent: MessageEvent<string>) => {
    try {
      onEvent(JSON.parse(nativeEvent.data) as AppEvent);
    } catch {
      return;
    }
  };

  source.onerror = () => undefined;

  source.addEventListener("peer_discovered", forward as EventListener);
  source.addEventListener("message_received", forward as EventListener);
  source.addEventListener("message_sent", forward as EventListener);
  source.addEventListener("chat_updated", forward as EventListener);
  source.addEventListener("chat_read", forward as EventListener);
  source.addEventListener("contact_added", forward as EventListener);
  source.addEventListener("contact_updated", forward as EventListener);
  source.addEventListener("contact_deleted", forward as EventListener);
  source.addEventListener("peer_blocked", forward as EventListener);
  source.addEventListener("peer_unblocked", forward as EventListener);
  source.addEventListener("error", forward as EventListener);

  return () => {
    source.close();
  };
}
