import { EventEmitter } from 'events';

export interface MonitorEvent {
  type: string;
  timestamp: string;
  data: Record<string, unknown>;
}

class MonitorEventBus extends EventEmitter {
  emitMonitor(data: MonitorEvent): boolean {
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
    type,
    timestamp: new Date().toISOString(),
    data,
  });
}
