import { DatabaseConfig } from "./client.js";

export function getDatabaseConfig(): DatabaseConfig {
  const url = process.env.LIBSQL_URL || 'file:/memory-tool.db';
  const authToken = process.env.LIBSQL_AUTH_TOKEN;
  
  return {
    url,
    authToken,
  };
}
