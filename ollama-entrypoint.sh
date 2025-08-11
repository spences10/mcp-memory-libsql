#!/bin/sh
ollama serve &
# Give the API a moment to start
sleep 5

# Check if the model is already downloaded
if ! ollama list | grep -q "nomic-embed-text"; then
    ollama pull nomic-embed-text
fi

# Do not run the model interactively; the server will lazily load on /api/embed

wait
