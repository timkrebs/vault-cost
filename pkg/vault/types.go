package vault

// ClientRecord is one row of the Vault Activity Export API (NDJSON).
// Only the fields relevant to per-namespace client costing are modeled.
type ClientRecord struct {
	ClientID      string `json:"client_id"`
	NamespaceID   string `json:"namespace_id"`
	NamespacePath string `json:"namespace_path"`
	MountAccessor string `json:"mount_accessor"`
	ClientType    string `json:"client_type"`
}

// Namespace returns the grouping key for the record. The Vault root namespace
// reports an empty namespace_path; it is normalized to "root" so it can be
// mapped via NAMESPACE_TEAM_MAPPING.
func (r ClientRecord) Namespace() string {
	if r.NamespacePath != "" {
		return r.NamespacePath
	}
	if r.NamespaceID != "" && r.NamespaceID != "root" {
		return r.NamespaceID
	}
	return "root"
}

// ClientTypeLabel normalizes the raw Vault client_type into a short, stable
// label used as the cost breakdown dimension. Vault counts these types
// separately (entity, non-entity, acme, secret-sync); unknown values pass
// through unchanged.
func (r ClientRecord) ClientTypeLabel() string {
	switch r.ClientType {
	case "entity":
		return "entity"
	case "non-entity-token", "non-entity", "nonentity":
		return "non-entity"
	case "pki-acme", "acme":
		return "acme"
	case "secret-sync", "secret_sync", "secret-sync-association":
		return "secret-sync"
	case "":
		return "unknown"
	default:
		return r.ClientType
	}
}
