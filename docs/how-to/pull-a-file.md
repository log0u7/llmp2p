# Pull a single file from a repository

You already know llmp2p basics and want one artifact (for example one quantized
GGUF) without pulling the whole repository.

Single-file pulls always use HTTPS from the Hub: they skip the swarm and the
index, because a partial file set would have a different infohash than the
full-repo swarm.

## Steps

1. Find the exact path in the repo (same as displayed on huggingface.co):

   ```sh
   llmp2p pull hf:Qwen/Qwen3-Coder-30B-A3B-GGUF#Qwen3-Coder-30B-A3B-Q4_K_M.gguf
   ```

2. The file lands in the model directory like any other file:

   ```sh
   ls ~/.local/share/llmp2p/store/Qwen/Qwen3-Coder-30B-A3B-GGUF/
   ```

## Notes

- `@revision` works the same way: `hf:org/repo@1a2b3c#path/to/file.gguf`.
- Single-file pulls are not published to the local index (they would shadow the
  full-repo entry); pull the whole repo once if you want to seed.
- LFS files are verified against the Hub sha256; small files are verified against
  the generated manifest afterwards.

Related: [reference/cli.md](../reference/cli.md) for the full grammar.
