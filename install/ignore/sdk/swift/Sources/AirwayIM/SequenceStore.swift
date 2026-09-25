import Foundation

/// Key-value seam for persisting per-conversation last-seen sequences; the
/// default client-side store is UserDefaultsSequenceStore. Pass nil to
/// keep cursors in memory only.
public protocol SequenceStore: Sendable {
    func get(_ key: String) -> String?
    func set(_ key: String, value: String)
    func remove(_ key: String)
}

public struct InMemorySequenceStore: SequenceStore {
    private let box = LockedBox<[String: String]>([:])

    public init() {}

    public func get(_ key: String) -> String? {
        box.current[key]
    }

    public func set(_ key: String, value: String) {
        var current = box.current
        current[key] = value
        box.set(current)
    }

    public func remove(_ key: String) {
        var current = box.current
        current[key] = nil
        box.set(current)
    }
}

/// Stores values in the standard defaults database — the natural choice in
/// iOS/macOS apps.
public struct UserDefaultsSequenceStore: SequenceStore, @unchecked Sendable {
    private let defaults: UserDefaults

    public init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    public func get(_ key: String) -> String? {
        defaults.string(forKey: key)
    }

    public func set(_ key: String, value: String) {
        defaults.set(value, forKey: key)
    }

    public func remove(_ key: String) {
        defaults.removeObject(forKey: key)
    }
}
