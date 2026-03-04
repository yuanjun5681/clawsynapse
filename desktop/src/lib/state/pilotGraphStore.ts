import { derived, writable, type Readable } from 'svelte/store';
import { canonicalizeNodeId } from '../domain/pilot/pilot-events';

interface PilotGraphState {
  selectedNodeId: string | null;
  knownNodeIds: string[];
  unmappedNodeIds: string[];
}

const initialState: PilotGraphState = {
  selectedNodeId: null,
  knownNodeIds: [],
  unmappedNodeIds: [],
};

const store = writable<PilotGraphState>(initialState);

function unique(values: string[]): string[] {
  return Array.from(new Set(values));
}

export const pilotGraphStore = {
  subscribe: store.subscribe,

  setSelectedNode(nodeId: string | null): void {
    const normalizedNodeId = nodeId ? canonicalizeNodeId(nodeId) : null;
    store.update((state) => ({
      ...state,
      selectedNodeId: normalizedNodeId,
    }));
  },

  syncKnownNodes(nodeIds: string[]): void {
    const normalized = unique(
      nodeIds
        .filter((id) => id && id.trim().length > 0)
        .map((id) => canonicalizeNodeId(id)),
    );
    const knownSet = new Set(normalized);

    store.update((state) => ({
      ...state,
      knownNodeIds: normalized,
      unmappedNodeIds: state.unmappedNodeIds.filter((id) => !knownSet.has(id)),
    }));
  },

  ensureNodeExists(nodeId: string): void {
    const normalizedNodeId = canonicalizeNodeId(nodeId);
    if (!normalizedNodeId || normalizedNodeId === 'unknown') return;

    store.update((state) => {
      if (
        state.knownNodeIds.includes(normalizedNodeId) ||
        state.unmappedNodeIds.includes(normalizedNodeId)
      ) {
        return state;
      }
      return {
        ...state,
        unmappedNodeIds: [...state.unmappedNodeIds, normalizedNodeId],
      };
    });
  },

  clear(): void {
    store.set(initialState);
  },
};

export function selectIsNodeKnown(nodeId: string): Readable<boolean> {
  const normalizedNodeId = canonicalizeNodeId(nodeId);
  return derived(store, ($state) => {
    return (
      $state.knownNodeIds.includes(normalizedNodeId) ||
      $state.unmappedNodeIds.includes(normalizedNodeId)
    );
  });
}
