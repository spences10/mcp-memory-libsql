import { DatabaseManager } from '../client.js';
import { get_database_config } from '../config.js';
import { schema } from './schema.js';

async function run_migrations() {
	const config = get_database_config();
	const db_manager = await DatabaseManager.get_instance(config);
	const db = db_manager.get_client();

	try {
		console.log('Starting migrations...');

		for (const migration of schema) {
			console.log(`Executing: ${migration.slice(0, 50)}...`);
			await db.execute(migration);
		}

		console.log('Migrations completed successfully');
	} catch (error) {
		console.error('Error running migrations:', error);
		throw error;
	}
}

// Run migrations if this file is executed directly
if (require.main === module) {
	run_migrations()
		.then(() => process.exit(0))
		.catch((error) => {
			console.error(error);
			process.exit(1);
		});
}

export { run_migrations };
