# Use the official Node.js image as the base image for building the application
FROM node:22.13.0 AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy package.json and pnpm-lock.yaml to the working directory
COPY package.json pnpm-lock.yaml ./

# Install PNPM, a fast package manager for Node.js projects
RUN npm install -g pnpm

# Install the project dependencies
RUN pnpm install --frozen-lockfile

# Copy the rest of the project files to the working directory
COPY . .

# Build the project
RUN pnpm run build

# Use a smaller Node.js image for the final application image
FROM node:22.13.0-slim

# Set the working directory inside the container
WORKDIR /app

# Copy the built files from the builder image
COPY --from=builder /app/dist /app/dist
COPY --from=builder /app/package.json /app/pnpm-lock.yaml /app/node_modules/ ./ 

# Set the environment variable for the database URL
ENV LIBSQL_URL="file:/memory-tool.db"

# Expose the port that the application will run on
EXPOSE 8080

# Define the command to run the application
CMD ["node", "dist/index.js"]