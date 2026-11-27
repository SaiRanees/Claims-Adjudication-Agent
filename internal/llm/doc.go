// Package llm is the one interface every model call goes through, with an
// adapter per provider. Each adapter declares what it supports, such as native
// PDF input, schema-constrained output, strict tool inputs, citations and prompt
// caching.
//
// A Client runs in one of three modes (ADR 0014): live calls the provider,
// record calls it only for requests without a cassette and writes one, and
// replay reads cassettes only. A request is identified by its hash, which
// covers the provider, model, job, prompt, schema, parameters, each content
// part's identity and a sample index. The worker accepts only live.
//
// The first adapter, in package gemini, uses Gemini's free tier (ADR 0016).
// models.yaml sets each job's model and thinking level and each model's
// limits and prices; a Limiter keeps calls under those limits, counting the
// day's requests in a file so a run cut off by the daily limit resumes the
// next day. Outputs are checked against their schema and decoded strictly.
// From week 4, the workflow's calls are also recorded in model_calls.
package llm
