import Foundation
@testable import AirwayIM

/// Rule-based stub HTTP server for the SDK tests — plain POSIX sockets,
/// one connection per request (`Connection: close`), mirroring the Ruby
/// and PHP suites' stdlib-only approach. The handler runs on a
/// per-connection thread; capture state through a locked box.
final class StubHTTPServer: @unchecked Sendable {
    struct Request {
        let method: String
        /// Raw request target, e.g. `/api/v1/conversations/01%20AB?type=group`.
        let target: String
        /// Percent-decoded path without query.
        let path: String
        let query: [String: String]
        /// Header names lowercased.
        let headers: [String: String]
        let body: Data

        func jsonBody() throws -> [String: Any] {
            try JSONSerialization.jsonObject(with: body) as? [String: Any] ?? [:]
        }
    }

    struct Response {
        var status: Int = 200
        var headers: [String: String] = ["Content-Type": "application/json"]
        var body: Data = Data()

        init(status: Int = 200, headers: [String: String] = ["Content-Type": "application/json"], body: Data = Data()) {
            self.status = status
            self.headers = headers
            self.body = body
        }

        /// Any-JSON convenience (dicts, arrays, `nil` for JSON null).
        init(status: Int = 200, json: Any?) {
            self.status = status
            if let json {
                body = try! JSONSerialization.data(withJSONObject: json)
            }
        }
    }

    typealias Handler = @Sendable (Request) throws -> Response

    private let handler: Handler
    private let lock = NSLock()
    private var serverFD: Int32 = -1
    private var boundPort: UInt16 = 0
    private var acceptThread: Thread?

    /// Drops the next N incoming connections without responding —
    /// simulates network failures the SDK should treat as status 0.
    private let dropsRemaining: LockedBox<Int> = LockedBox(0)

    var port: UInt16 { boundPort }
    var baseURL: String { "http://127.0.0.1:\(boundPort)" }

    init(handler: @escaping Handler) throws {
        self.handler = handler

        let fd = socket(AF_INET, SOCK_STREAM, 0)
        guard fd >= 0 else { throw stubError("socket() failed") }
        var yes: Int32 = 1
        _ = setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &yes, socklen_t(MemoryLayout<Int32>.size))

        var addr = sockaddr_in()
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = 0 // let the OS pick a port
        addr.sin_addr = in_addr(s_addr: INADDR_LOOPBACK.bigEndian)

        let bindResult = withUnsafePointer(to: &addr) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                bind(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        guard bindResult == 0 else {
            close(fd)
            throw stubError("bind() failed")
        }
        guard listen(fd, 16) == 0 else {
            close(fd)
            throw stubError("listen() failed")
        }

        var bound = sockaddr_in()
        var length = socklen_t(MemoryLayout<sockaddr_in>.size)
        _ = withUnsafeMutablePointer(to: &bound) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                getsockname(fd, $0, &length)
            }
        }
        serverFD = fd
        boundPort = UInt16(bigEndian: bound.sin_port)
    }

    func start() {
        let thread = Thread { [weak self] in
            self?.acceptLoop()
        }
        thread.name = "stub-http-server"
        thread.start()
        lock.lock()
        acceptThread = thread
        lock.unlock()
    }

    /// Stop accepting and drop the listener. In-flight requests finish.
    func stop() {
        lock.lock()
        let fd = serverFD
        serverFD = -1
        lock.unlock()
        if fd >= 0 { close(fd) }
    }

    /// Drop the next N incoming connections without responding —
    /// simulates network failures the SDK should treat as status 0.
    func setDrops(_ count: Int) {
        dropsRemaining.set(count)
    }

    // ---- Plumbing ----

    private func acceptLoop() {
        while true {
            lock.lock()
            let fd = serverFD
            lock.unlock()
            guard fd >= 0 else { return }
            let client = accept(fd, nil, nil)
            guard client >= 0 else { return } // listener closed
            Thread { [weak self] in
                self?.serve(client)
            }.start()
        }
    }

    private func serve(_ fd: Int32) {
        defer { close(fd) }

        var timeout = timeval(tv_sec: 10, tv_usec: 0)
        _ = setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))

        guard let request = readRequest(fd: fd) else { return }
        let drops = dropsRemaining.current
        if drops > 0 {
            dropsRemaining.set(drops - 1)
            return // drop without responding
        }
        let response: Response
        do {
            response = try handler(request)
        } catch {
            response = Response(status: 500, body: Data("handler error: \(error)".utf8))
        }
        write(response: response, fd: fd)
    }

    private func readRequest(fd: Int32) -> Request? {
        var buffer = Data()
        let chunkSize = 65_536
        var chunk = [UInt8](repeating: 0, count: chunkSize)
        let headerEnd = Data("\r\n\r\n".utf8)

        // Read until end of headers.
        var headerRange: Range<Data.Index>?
        while headerRange == nil {
            let n = recv(fd, &chunk, chunkSize, 0)
            guard n > 0 else { return nil }
            buffer.append(contentsOf: chunk[0..<n])
            headerRange = buffer.range(of: headerEnd)
            if buffer.count > 1_048_576 { return nil }
        }
        guard let range = headerRange else { return nil }

        let headerText = String(decoding: buffer[..<range.lowerBound], as: UTF8.self)
        var lines = headerText.split(separator: "\r\n", omittingEmptySubsequences: false).map(String.init)
        guard !lines.isEmpty else { return nil }
        let requestLine = lines.removeFirst()
        let parts = requestLine.split(separator: " ")
        guard parts.count >= 2 else { return nil }

        var headers: [String: String] = [:]
        for line in lines where !line.isEmpty {
            let pair = line.split(separator: ":", maxSplits: 1)
            guard pair.count == 2 else { continue }
            headers[pair[0].trimmed.lowercased()] = pair[1].trimmed
        }

        var body = Data(buffer[range.upperBound...])
        if let contentLength = headers["content-length"].flatMap(Int.init) {
            while body.count < contentLength {
                let n = recv(fd, &chunk, chunkSize, 0)
                guard n > 0 else { break }
                body.append(contentsOf: chunk[0..<n])
            }
            if body.count > contentLength {
                body = body.prefix(contentLength)
            }
        }

        let target = String(parts[1])
        let (rawPath, rawQuery) = targetSplit(target)
        return Request(
            method: String(parts[0]),
            target: target,
            path: decodePercent(rawPath),
            query: parseQuery(rawQuery),
            headers: headers,
            body: body)
    }

    private func write(response: Response, fd: Int32) {
        let reason = responseReason(response.status)
        var head = "HTTP/1.1 \(response.status) \(reason)\r\n"
        for (name, value) in response.headers {
            head += "\(name): \(value)\r\n"
        }
        head += "Content-Length: \(response.body.count)\r\n"
        head += "Connection: close\r\n\r\n"
        var payload = Data(head.utf8)
        payload.append(response.body)
        payload.withUnsafeBytes { pointer in
            _ = send(fd, pointer.baseAddress, pointer.count, 0)
        }
    }
}

private func responseReason(_ status: Int) -> String {
    switch status {
    case 200: return "OK"
    case 201: return "Created"
    case 204: return "No Content"
    case 400: return "Bad Request"
    case 401: return "Unauthorized"
    case 403: return "Forbidden"
    case 404: return "Not Found"
    case 500: return "Internal Server Error"
    default: return "Status"
    }
}

private func targetSplit(_ target: String) -> (path: String, query: String?) {
    guard let question = target.firstIndex(of: "?") else {
        return (target, nil)
    }
    return (String(target[..<question]), String(target[target.index(after: question)...]))
}

private func parseQuery(_ query: String?) -> [String: String] {
    guard let query else { return [:] }
    var result: [String: String] = [:]
    for pair in query.split(separator: "&") {
        let kv = pair.split(separator: "=", maxSplits: 1).map(String.init)
        guard let key = kv.first else { continue }
        result[decodePercent(key)] = kv.count > 1 ? decodePercent(kv[1]) : ""
    }
    return result
}

private func decodePercent(_ string: String) -> String {
    string.replacingOccurrences(of: "+", with: "%20")
        .removingPercentEncoding ?? string
}

private func stubError(_ message: String) -> NSError {
    NSError(domain: "StubHTTPServer", code: 1, userInfo: [NSLocalizedDescriptionKey: message])
}

private extension Substring {
    var trimmed: String {
        String(self).trimmingCharacters(in: .whitespaces)
    }
}

// ---- Shared helpers ----

/// Shorthand for building envelope-carrying responses.
typealias StubResponse = StubHTTPServer.Response

/// `{code, data, message}` envelope builder for stub responses.
func envelope(_ data: Any?, code: Int = 0, message: String? = nil) -> Any {
    var object: [String: Any] = ["code": code, "data": data ?? NSNull()]
    if let message { object["message"] = message }
    return object
}

/// A locked box tests can capture into from server-handler threads.
final class Capture<T>: @unchecked Sendable {
    private let lock = NSLock()
    private var items: [T] = []

    func append(_ item: T) {
        lock.lock()
        items.append(item)
        lock.unlock()
    }

    var all: [T] {
        lock.lock()
        defer { lock.unlock() }
        return items
    }

    var count: Int { all.count }

    var last: T? { all.last }

    var first: T? { all.first }
}
