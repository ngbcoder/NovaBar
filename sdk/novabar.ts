/**
 * NovaBar Widget SDK
 * Lightweight TypeScript/JavaScript helper for talking to the Go daemon over JSON-RPC 2.0
 */

export interface RPCResponse<T = any> {
    jsonrpc: "2.0";
    id?: number | string;
    result?: T;
    error?: {
        code: number;
        message: string;
    };
}

export type EventHandler = (data: any) => void;

export class NovaBarSDK {
    private ws: WebSocket | null = null;
    private idCounter = 1;
    private pendingRequests = new Map<number | string, { resolve: Function; reject: Function }>();
    private listeners = new Map<string, Set<EventHandler>>();

    constructor(private port: number = 9876) {}

    public connect(): Promise<void> {
        return new Promise((resolve, reject) => {
            this.ws = new WebSocket(`ws://127.0.0.1:${this.port}/ws`);

            this.ws.onopen = () => {
                console.log("[NovaBar SDK] Connected to daemon");
                resolve();
            };

            this.ws.onerror = (err) => {
                console.error("[NovaBar SDK] Connection error:", err);
                reject(err);
            };

            this.ws.onmessage = (event) => {
                try {
                    const data = JSON.parse(event.data);
                    if (data.id !== undefined && this.pendingRequests.has(data.id)) {
                        const { resolve, reject } = this.pendingRequests.get(data.id)!;
                        this.pendingRequests.delete(data.id);
                        if (data.error) {
                            reject(new Error(data.error.message));
                        } else {
                            resolve(data.result);
                        }
                    } else if (data.method && this.listeners.has(data.method)) {
                        for (const handler of this.listeners.get(data.method)!) {
                            handler(data.params);
                        }
                    }
                } catch (e) {
                    console.error("[NovaBar SDK] Failed to parse message:", e);
                }
            };
        });
    }

    public call<T = any>(method: string, params: any = {}): Promise<T> {
        if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
            return Promise.reject(new Error("WebSocket is not connected"));
        }
        const id = this.idCounter++;
        const payload = {
            jsonrpc: "2.0",
            id,
            method,
            params,
        };
        return new Promise<T>((resolve, reject) => {
            this.pendingRequests.set(id, { resolve, reject });
            this.ws!.send(JSON.stringify(payload));
        });
    }

    public subscribe(topic: string, handler: EventHandler): void {
        if (!this.listeners.has(topic)) {
            this.listeners.set(topic, new Set());
        }
        this.listeners.get(topic)!.add(handler);
    }

    public unsubscribe(topic: string, handler: EventHandler): void {
        if (this.listeners.has(topic)) {
            this.listeners.get(topic)!.delete(handler);
        }
    }
}
