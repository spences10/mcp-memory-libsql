import { DatabaseManager } from "../client.js";
import { getDatabaseConfig } from "../config.js";
import { schema } from "./schema.js";

async function runMigrations() {
  const config = getDatabaseConfig();
  const dbManager = await DatabaseManager.getInstance(config);
  const db = dbManager.getClient();

  try {
    console.log("Starting migrations...");

    for (const migration of schema) {
      console.log(`Executing: ${migration.slice(0, 50)}...`);
      await db.execute(migration);
    }

    console.log("Migrations completed successfully");
  } catch (error) {
    console.error("Error running migrations:", error);
    throw error;
  }
}

// Run migrations if this file is executed directly
if (require.main === module) {
  runMigrations()
    .then(() => process.exit(0))
    .catch((error) => {
      console.error(error);
      process.exit(1);
    });
}

export { runMigrations };
