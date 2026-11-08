import { readFile, writeFile, mkdir, copyFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'

const repo = new URL('../', import.meta.url)
const plugins = []
for (const provider of ['codex', 'claude', 'antigravity']) {
  const key = `${provider}-oauth`
  const canonical = new URL(`plugins/personal-tasks/${key}/`, repo)
  const source = await readFile(new URL('plugin.js', canonical), 'utf8')
  const { meta } = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`)
  const destination = new URL(`web/public/oauth-marketplace/plugins/${key}/${meta.version}/`, repo)
  await mkdir(destination, { recursive: true })
  await copyFile(new URL('plugin.js', canonical), new URL('plugin.js', destination))
  await copyFile(new URL('fixtures.json', canonical), new URL('fixtures.json', destination))
  plugins.push({ key, name: meta.name, description: meta.description.en, models: meta.models, latest: meta.version, versions: [{ version: meta.version, path: `plugins/${key}/${meta.version}/plugin.js`, sha256: createHash('sha256').update(source).digest('hex'), minApiVersion: 1, auth: 'oauth2_pkce' }] })
}
const index = new URL('web/public/oauth-marketplace/index.json', repo)
await writeFile(index, `${JSON.stringify({ indexVersion: 1, name: 'Aksara OAuth Providers', plugins }, null, 2)}\n`)
console.log(`Updated ${fileURLToPath(index)} (${plugins.length} providers)`)
