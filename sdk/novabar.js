/**
 * NovaBar widget SDK.
 *
 * Talks to the Go daemon over JSON-RPC 2.0 on the loopback WebSocket. The endpoint and
 * session token are injected by the daemon as `window.NOVABAR` before any widget code
 * runs, so nothing here needs to be configured and no credentials live in a file.
 *
 * Everything is ESM and uses no build step: a widget is a directory with an index.html.
 */

/** One pending JSON-RPC call. */
class PendingCall {
    constructor(resolve, reject) {
        this.resolve = resolve;
        this.reject = reject;
    }
}

export class NovaBarSDK {
    /**
     * @param {{port?: number, token?: string, url?: string}} options
     */
    constructor(options = {}) {
        this.port = options.port;
        this.token = options.token;
        this.url = options.url;

        this.ws = null;
        this.nextId = 1;
        /** @type {Map<number|string, PendingCall>} */
        this.pending = new Map();
        /** @type {Map<string, Set<Function>>} */
        this.listeners = new Map();

        this.connectionPromise = null;
    }

    /** True once connected. */
    get connected() {
        return this.ws !== null && this.ws.readyState === WebSocket.OPEN;
    }

    /** Endpoint, preferring the injected settings over a bare port. */
    endpoint() {
        if (this.url) return this.url;
        if (!this.port) throw new Error('NovaBarSDK: no port or url supplied');
        return `ws://127.0.0.1:${this.port}/ws?token=${encodeURIComponent(this.token ?? '')}`;
    }

    /**
     * Open the WebSocket. Concurrent calls share one connection.
     */
    connect() {
        if (this.connectionPromise) return this.connectionPromise;

        this.connectionPromise = new Promise((resolve, reject) => {
            let ws;
            try {
                ws = new WebSocket(this.endpoint());
            } catch (err) {
                this.connectionPromise = null;
                reject(err);
                return;
            }

            this.ws = ws;

            ws.onopen = () => {
                console.info('[NovaBar SDK] connected');
                // Re-subscribe on every reconnect: the server forgets a client's
                // subscriptions when its connection drops.
                this.resubscribe();
                resolve(this);
            };

            ws.onerror = (err) => {
                if (this.connectionPromise) {
                    this.connectionPromise = null;
                    reject(err);
                }
            };

            ws.onclose = () => {
                this.failAllPending(new Error('NovaBar SDK: connection closed'));
                this.ws = null;
                this.connectionPromise = null;
            };

            ws.onmessage = (event) => this.dispatch(event.data);
        });

        return this.connectionPromise;
    }

    dispatch(raw) {
        let message;
        try {
            message = JSON.parse(raw);
        } catch (err) {
            console.warn('[NovaBar SDK] malformed message', err);
            return;
        }

        if (message.id !== undefined && message.id !== null) {
            const pending = this.pending.get(message.id);
            if (!pending) return;
            this.pending.delete(message.id);
            if (message.error) {
                pending.reject(new NovaBarError(message.error.code, message.error.message));
            } else {
                pending.resolve(message.result);
            }
            return;
        }

        if (message.method) {
            for (const handler of this.listeners.get(message.method) ?? []) {
                try {
                    handler(message.params);
                } catch (err) {
                    console.error(`[NovaBar SDK] handler for ${message.method} threw`, err);
                }
            }
        }
    }

    failAllPending(err) {
        for (const pending of this.pending.values()) pending.reject(err);
        this.pending.clear();
    }

    /**
     * Issue a JSON-RPC call.
     *
     * @param {string} method
     * @param {object} [params]
     * @returns {Promise<any>}
     */
    call(method, params = {}) {
        if (!this.connected) {
            return Promise.reject(new Error('NovaBar SDK: not connected'));
        }
        const id = this.nextId++;
        return new Promise((resolve, reject) => {
            this.pending.set(id, new PendingCall(resolve, reject));
            this.ws.send(JSON.stringify({ jsonrpc: '2.0', id, method, params }));
        });
    }

    /**
     * Subscribe to a server-pushed topic. Topics are opt-in on the server side, so this
     * is also what causes the daemon to start sending them.
     */
    subscribe(topic, handler) {
        if (!this.listeners.has(topic)) this.listeners.set(topic, new Set());
        this.listeners.get(topic).add(handler);
        if (this.connected) this.sendSubscription('subscribe', topic);
    }

    /** Remove a handler for a topic. */
    unsubscribe(topic, handler) {
        const set = this.listeners.get(topic);
        if (set) set.delete(handler);
    }

    sendSubscription(action, topic) {
        if (!this.connected) return;
        const id = this.nextId++;
        this.pending.set(id, new PendingCall(
            () => {},
            (err) => console.warn(`[NovaBar SDK] ${action} ${topic} failed`, err),
        ));
        this.ws.send(JSON.stringify({
            jsonrpc: '2.0',
            id,
            method: action,
            params: { topic },
        }));
    }

    /** Re-send every subscription after a reconnect. */
    resubscribe() {
        for (const topic of this.listeners.keys()) {
            this.sendSubscription('subscribe', topic);
        }
    }
}

/** An error returned by the daemon, carrying its JSON-RPC code. */
export class NovaBarError extends Error {
    constructor(code, message) {
        super(message);
        this.name = 'NovaBarError';
        this.code = code;
    }
}

export default NovaBarSDK;