package plan

// ImportBlock is a declarative `import {}` block: adopt the resource at Address
// (which must have a resource block) using the remote id ID.
type ImportBlock struct {
	Address string
	ID      string
}
