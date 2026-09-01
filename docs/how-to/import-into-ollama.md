# Import a pulled model into Ollama

You pulled a GGUF with llmp2p and want to chat with it in Ollama.

## Steps

1. Import (the store holds the file):

   ```sh
   llmp2p import hf:HuggingFaceTB/SmolLM2-135M-Instruct-GGUF
   ```

   Default Ollama name is the repo basename (here `smollm2-135m-instruct-gguf`).
   Choose your own with `--name smollm2:135m`.

2. Run it:

   ```sh
   ollama run smollm2:135m
   ```

## Remote Ollama

Point at another daemon:

```sh
llmp2p import hf:org/repo --host tcp://gpu-box.lan:11434
```

## Troubleshooting

- `no .gguf file found in model directory`: the repo is not a GGUF repo (for
  example safetensors only). Pull a GGUF variant repo instead.
- `split GGUF is not importable`: the repo stores `-00001-of-00002.gguf`
  shards; Ollama cannot import those. Find a single-file GGUF.
- `ollama: create ...`: read the streamed output; most failures are Ollama-side
  (disk space, corrupted model metadata).

Under the hood the command writes a `FROM <absolute gguf path>` Modelfile and
runs `ollama create`: it never touches Ollama's blob storage directly.
