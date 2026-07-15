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
