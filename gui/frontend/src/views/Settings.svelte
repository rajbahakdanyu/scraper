<script lang="ts">
  import { onMount } from 'svelte'
  import {
      ApplySettings,
      ChooseCookiesFile,
      ChooseLogFile,
      ChooseOutputDir,
      GetOptions,
      GetSettings,
      ReloadSettings,
      SaveSettings,
  } from '../../wailsjs/go/backend/App'
  import type { backend } from '../../wailsjs/go/models'
  import { splitList } from '../lib/format'
  import { errorText, toast } from '../lib/state.svelte'

  let s = $state<backend.Settings | null>(null)
  let opts = $state<backend.Options | null>(null)
  let lists = $state({ include: '', exclude: '', ext: '' })
  let error = $state('')

  onMount(async () => {
    try {
      opts = await GetOptions()
      load(await GetSettings())
    } catch (err) {
      error = errorText(err)
    }
  })

  function load(next: backend.Settings) {
    s = next
    lists = { include: next.include.join(', '), exclude: next.exclude.join(', '), ext: next.mediaExtensions.join(', ') }
    error = ''
  }

  function collect(): backend.Settings {
    return { ...s!, include: splitList(lists.include), exclude: splitList(lists.exclude), mediaExtensions: splitList(lists.ext) } as backend.Settings
  }

  async function save(persist: boolean) {
    error = ''
    try {
      const next = collect()
      await (persist ? SaveSettings(next) : ApplySettings(next))
      toast(persist ? `Saved to ${opts?.configPath}` : 'Settings applied to the next run')
    } catch (err) {
      error = errorText(err)
    }
  }

  async function revert() {
    try {
      load(await ReloadSettings())
    } catch (err) {
      error = errorText(err)
    }
  }

  async function pick(fn: () => Promise<string>, set: (v: string) => void) {
    try {
      const v = await fn()
      if (v) set(v)
    } catch (err) {
      error = errorText(err)
    }
  }
</script>

{#if s}
  <section class="settings">
    <div class="row">
      <h2 class="grow">Settings</h2>
      <button onclick={revert} title="Discard unsaved changes">Revert</button>
      <button onclick={() => save(false)} title="Use for the next run without saving">Apply</button>
      <button class="primary" onclick={() => save(true)}>Save</button>
    </div>
    <p class="dim small">Saved to <span class="mono">{opts?.configPath}</span> — the CLI and TUI read the same file.</p>
    {#if error}<p class="err">{error}</p>{/if}

    <fieldset class="panel">
      <legend>Downloads</legend>
      <label>Output folder
        <span class="row"><input class="grow" type="text" bind:value={s.outputDir} /><button onclick={() => pick(ChooseOutputDir, (v) => (s!.outputDir = v))}>Choose…</button></span>
      </label>
      <label>Concurrent downloads <input type="number" min="1" max="256" bind:value={s.concurrency} /></label>
      <label>Speed limit <input type="text" bind:value={s.rateLimit} placeholder="unlimited (e.g. 500K, 2M)" /></label>
      <label class="check"><input type="checkbox" bind:checked={s.checksums} /> Write and verify .sha256 checksum files</label>
      <label class="check"><input type="checkbox" bind:checked={s.diskCheck} /> Warn when free disk space is short</label>
      <label class="check"><input type="checkbox" bind:checked={s.notify} /> Notify when downloads complete</label>
    </fieldset>

    <fieldset class="panel">
      <legend>Filters</legend>
      <label class="check"><input type="checkbox" bind:checked={s.mediaOnly} /> Only media and archive files</label>
      <label>Extra allowed extensions <input type="text" bind:value={lists.ext} placeholder="cbz, iso" /></label>
      <label>Include globs <input type="text" bind:value={lists.include} placeholder="*.mp4, *.mkv" /></label>
      <label>Exclude globs <input type="text" bind:value={lists.exclude} placeholder="*.txt" /></label>
      <div class="pair">
        <label>Min size <input type="text" bind:value={s.minSize} placeholder="500K" /></label>
        <label>Max size <input type="text" bind:value={s.maxSize} placeholder="2G" /></label>
      </div>
      <div class="pair">
        <label>Max items per album <input type="number" min="0" bind:value={s.maxAlbumItems} /></label>
        <label>Delay between album scrapes <input type="text" bind:value={s.scrapeDelay} placeholder="none (e.g. 500ms)" /></label>
      </div>
    </fieldset>

    <fieldset class="panel">
      <legend>YouTube</legend>
      <div class="pair">
        <label>Max quality
          <select bind:value={s.quality} disabled={!!s.audioOnly}>
            <option value="">Best available</option>
            {#each opts?.qualities ?? [] as q (q)}<option value={q}>{q}</option>{/each}
          </select>
        </label>
        <label>Audio only
          <select bind:value={s.audioOnly} disabled={!!s.quality}>
            <option value="">Off</option>
            {#each opts?.audioFormats ?? [] as f (f)}<option value={f}>{f}</option>{/each}
          </select>
        </label>
      </div>
    </fieldset>

    <fieldset class="panel">
      <legend>Network</legend>
      <label>Proxy <input type="text" bind:value={s.proxy} placeholder="http://proxy:8080" /></label>
      <label>Cookies file (Netscape format)
        <span class="row"><input class="grow" type="text" bind:value={s.cookies} /><button onclick={() => pick(ChooseCookiesFile, (v) => (s!.cookies = v))}>Choose…</button></span>
      </label>
      <label>Cookies from browser
        <select bind:value={s.cookiesFromBrowser}>
          <option value="">None</option>
          {#each opts?.browsers ?? [] as b (b)}<option value={b}>{b}</option>{/each}
        </select>
      </label>
      <div class="pair">
        <label>Response timeout <input type="text" bind:value={s.timeout} placeholder="30s" /></label>
        <label>User agent <input type="text" bind:value={s.userAgent} /></label>
      </div>
    </fieldset>

    <fieldset class="panel">
      <legend>Logging</legend>
      <label>Run log file
        <span class="row"><input class="grow" type="text" bind:value={s.logFile} placeholder="none" /><button onclick={() => pick(ChooseLogFile, (v) => (s!.logFile = v))}>Choose…</button></span>
      </label>
    </fieldset>
  </section>
{/if}

<style>
  .settings {
    max-width: 820px;
    margin: 0 auto;
    padding: 18px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  h2 {
    margin: 0;
    font-size: 18px;
  }
  .small {
    font-size: 12px;
    margin: 0;
  }
  fieldset {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 12px 14px;
  }
  legend {
    color: var(--accent);
    padding: 0 4px;
  }
  label {
    display: flex;
    flex-direction: column;
    gap: 4px;
    color: var(--dim);
    font-size: 12px;
  }
  label > input,
  label > select,
  label > span {
    color: var(--text);
    font-size: 14px;
  }
  label.check {
    flex-direction: row;
    align-items: center;
    color: var(--text);
    font-size: 14px;
  }
  .pair {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }
</style>
