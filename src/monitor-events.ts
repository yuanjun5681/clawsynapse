import { EventEmitter } from 'events';

export interface MonitorEvent {
  id: string;
  type: string;
  timestamp: string;
  data: Record<string, unknown>;
}

const MAX_MONITOR_EVENT_BUFFER = 1000;
let monitorEventSeq = 0;
const monitorEventBuffer: MonitorEvent[] = [];

class MonitorEventBus extends EventEmitter {
  emitMonitor(data: MonitorEvent): boolean {
    monitorEventBuffer.push(data);
    if (monitorEventBuffer.length > MAX_MONITOR_EVENT_BUFFER) {
      monitorEventBuffer.shift();
    }
    return this.emit('monitor', data);
  }

  onMonitor(listener: (data: MonitorEvent) => void): this {
    return this.on('monitor', listener);
  }
}

export const monitorBus = new MonitorEventBus();

export function emitMonitorEvent(
  type: string,
  data: Record<string, unknown> = {},
): void {
  monitorBus.emitMonitor({
    id: String(++monitorEventSeq),
    type,
    timestamp: new Date().toISOString(),
    data,
  });
}

export function getBufferedMonitorEvents(lastEventId?: string): MonitorEvent[] {
  if (!lastEventId) {
    return [...monitorEventBuffer];
  }

  const lastIdNum = Number(lastEventId);
  if (!Number.isFinite(lastIdNum)) {
    return [...monitorEventBuffer];
  }

  return monitorEventBuffer.filter((event) => Number(event.id) > lastIdNum);
}
