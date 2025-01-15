import { DatabaseConfig } from "./client.js";

export function get_database_config(): DatabaseConfig {
	const url = process.env.LIBSQL_URL || "file:./memory-tool.db";
	const auth_token = process.env.LIBSQL_AUTH_TOKEN;

	return {
		url,
		authToken: auth_token,
	};
}
