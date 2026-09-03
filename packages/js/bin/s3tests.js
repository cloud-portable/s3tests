#!/usr/bin/env node
import { run } from '../lib/cli.js'

process.exit(await run(process.argv.slice(2), process.stdout, process.stderr))
