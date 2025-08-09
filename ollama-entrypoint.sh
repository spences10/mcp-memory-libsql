#!/bin/sh
ollama serve &
sleep 10
ollama pull nomic-embed-text
wait