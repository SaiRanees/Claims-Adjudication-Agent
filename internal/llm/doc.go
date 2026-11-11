// Package llm is the one interface every model call goes through, with an
// adapter per provider. Each adapter declares what it supports, such as native
// PDF input, schema-constrained output, strict tool inputs, citations and prompt
// caching, and every call is recorded in model_calls.
package llm
