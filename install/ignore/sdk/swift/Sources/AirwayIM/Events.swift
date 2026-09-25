import Foundation

/// Cancellation handle returned by the various `on…` registrations; keep it
/// alive for as long as the listener should fire, or store it and call
/// `cancel()` to unsubscribe early.
public struct Subscription: Sendable {
    private let remove: @Sendable () -> Void

    init(remove: @escaping @Sendable () -> Void) {
        self.remove = remove
    }

    public func cancel() {
        remove()
    }
}

/// Multicast listener list: thread-safe add/remove/emit, so events can be
/// delivered from any executor without hopping through an actor.
final class ListenerList<Value>: @unchecked Sendable {
    private let lock = NSLock()
    private var listeners: [UUID: @Sendable (Value) -> Void] = [:]

    @discardableResult
    func add(_ listener: @escaping @Sendable (Value) -> Void) -> Subscription {
        let id = UUID()
        lock.lock()
        listeners[id] = listener
        lock.unlock()
        return Subscription { [weak self] in
            guard let self else { return }
            self.lock.lock()
            self.listeners[id] = nil
            self.lock.unlock()
        }
    }

    func emit(_ value: Value) {
        lock.lock()
        let snapshot = Array(listeners.values)
        lock.unlock()
        for listener in snapshot {
            listener(value)
        }
    }
}

/// A mutable value readable and writable from any executor.
final class LockedBox<Value>: @unchecked Sendable {
    private let lock = NSLock()
    private var value: Value

    init(_ value: Value) {
        self.value = value
    }

    var current: Value {
        lock.lock()
        defer { lock.unlock() }
        return value
    }

    func set(_ newValue: Value) {
        lock.lock()
        value = newValue
        lock.unlock()
    }
}
