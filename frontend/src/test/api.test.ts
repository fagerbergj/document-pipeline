import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// Intercept fetch to capture the URL.
let capturedURL = ''
beforeEach(() => {
  capturedURL = ''
  vi.stubGlobal('fetch', vi.fn((url: string) => {
    capturedURL = url
    return Promise.resolve({
      ok: true,
      json: () => Promise.resolve({ data: [], next_page_token: null }),
    })
  }))
})
afterEach(() => { vi.unstubAllGlobals() })

// Import after stubbing so the module uses the stubbed fetch.
async function documentsURL(params: Parameters<typeof import('../api').api.documents>[0]) {
  const { api } = await import('../api')
  await api.documents(params).catch(() => {})
  return capturedURL
}

describe('api.documents URL construction', () => {
  it('omits q when not provided', async () => {
    const url = await documentsURL({ sort: 'pipeline', page_size: 20 })
    expect(url).not.toContain('q=')
  })

  it('includes q when provided', async () => {
    const url = await documentsURL({ q: 'status:pending' })
    expect(url).toContain('q=status%3Apending')
  })

  it('includes sort and page_size', async () => {
    const url = await documentsURL({ sort: 'title_asc', page_size: 50 })
    expect(url).toContain('sort=title_asc')
    expect(url).toContain('page_size=50')
  })

  it('includes page_token when provided', async () => {
    const url = await documentsURL({ page_token: 'tok123' })
    expect(url).toContain('page_token=tok123')
  })

  it('combines q with sort', async () => {
    const url = await documentsURL({ q: 'tags:invoice', sort: 'pipeline' })
    expect(url).toContain('q=')
    expect(url).toContain('sort=pipeline')
  })
})
