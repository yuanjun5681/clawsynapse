import { writable } from 'svelte/store';
import { canonicalizeNodeId } from '../domain/pilot/pilot-events';

export interface ChatBubble {
  id: string;
  nodeId: string;
  text: string;
  direction: 'sent' | 'received';
  timestamp: string;
}

const BUBBLE_TTL_MS = 15000;

const store = writable<ChatBubble[]>([]);

let bubbleSeq = 0;
const timers = new Map<string, ReturnType<typeof setTimeout>>();

function removeBubble(id: string): void {
  timers.delete(id);
  store.update((bubbles) => bubbles.filter((b) => b.id !== id));
}

export const chatBubbleStore = {
  subscribe: store.subscribe,

  add(nodeId: string, text: string, direction: 'sent' | 'received'): void {
    const id = `bubble-${++bubbleSeq}`;
    const canonical = canonicalizeNodeId(nodeId);
    const bubble: ChatBubble = {
      id,
      nodeId: canonical,
      text,
      direction,
      timestamp: new Date().toISOString(),
    };

    store.update((bubbles) => {
      // Keep at most 3 bubbles per node
      const others = bubbles.filter((b) => b.nodeId !== canonical);
      const nodeOnes = bubbles.filter((b) => b.nodeId === canonical);
      const kept = nodeOnes.length >= 3 ? nodeOnes.slice(0, 2) : nodeOnes;
      return [...others, ...kept, bubble];
    });

    const timer = setTimeout(() => removeBubble(id), BUBBLE_TTL_MS);
    timers.set(id, timer);
  },

  clear(): void {
    for (const timer of timers.values()) {
      clearTimeout(timer);
    }
    timers.clear();
    store.set([]);
  },
};
