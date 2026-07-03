resource "mock_thing" "a" {
  name = "root"
}

resource "mock_ref" "b" {
  name = "child"
  # Unsupported: a reference wrapped in a string template (not a bare traversal).
  target = "prefix-${mock_thing.a.id}"
}
