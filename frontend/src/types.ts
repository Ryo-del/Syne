export type Contact = {
  name: string;
  peer_id: string;
  ip: string;
  port: string;
};

export type BlockedPeer = {
  name?: string;
  peer_id: string;
  added_at: number;
  reason?: string;
};

export type PeerPresence = {
  peer_id: string;
  name: string;
  addr: string;
  last_seen: number;
  blocked: boolean;
};

export type ChatSummary = {
  chat_id: string;
  peer_id: string;
  title: string;
  preview: string;
  last_timestamp: number;
  known_addr?: string;
  online: boolean;
  blocked: boolean;
  unread_count: number;
};

export type UIMessage = {
  message_id?: string;
  chat_id: string;
  target_id: string;
  from: string;
  text: string;
  timestamp: number;
  direction: "incoming" | "outgoing";
};

export type Snapshot = {
  local_id: string;
  port: number;
  contacts: Contact[];
  blocked: BlockedPeer[];
  neighbors: PeerPresence[];
  chats: ChatSummary[];
};

export type AppEvent = {
  type: string;
  timestamp: number;
  peer?: PeerPresence;
  chat?: ChatSummary;
  message?: UIMessage;
  contact?: Contact;
  blocked?: BlockedPeer;
  error?: string;
};
