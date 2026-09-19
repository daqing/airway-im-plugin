export declare function browserAdapter(): {
    http: {
        request(options: {
            url: string;
            method: "GET" | "POST" | "PUT" | "DELETE";
            headers?: Record<string, string>;
            data?: unknown;
            timeoutMs?: number;
        }): Promise<{
            statusCode: number;
            data: unknown;
        }>;
    };
    socket: (url: string) => {
        send: (data: string) => void;
        close: () => void;
        onOpen: (cb: () => void) => void;
        onMessage: (cb: (data: string) => void) => void;
        onClose: (cb: () => void) => void;
        onError: (cb: (err: Error) => void) => void;
    };
    upload: (options: {
        url: string;
        filePath: string | object;
        name: string;
        formData?: Record<string, string>;
        timeoutMs?: number;
    }) => Promise<{
        statusCode: number;
        data: unknown;
    }>;
    storage: {
        get: (key: string) => string | null;
        set: (key: string, value: string) => void;
        remove: (key: string) => void;
    };
    onShow: (cb: () => void) => void;
};
