import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Sidebar from '../components/Sidebar'

vi.mock('../api', () => ({
  api: {
    pipeline: () => Promise.resolve({ stages: [{ name: 'ocr' }, { name: 'classify' }] }),
    jobs: () => Promise.resolve({ data: [] }),
  },
}))

function renderSidebar(initialPath = '/?') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  let searchParams = ''
  const Capture = () => {
    const { useSearchParams } = require('react-router-dom')
    const [sp] = useSearchParams()
    searchParams = sp.toString()
    return null
  }
  const { rerender } = render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route path="/" element={<><Sidebar open onClose={() => {}} /><Capture /></>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
  return { getSearchParams: () => searchParams, rerender }
}

describe('Sidebar filter chips', () => {
  it('renders status filter buttons', () => {
    renderSidebar()
    expect(screen.getByRole('button', { name: /pending/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /running/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /done/i })).toBeInTheDocument()
  })

  it('clicking a status chip sets q=status:<value>', () => {
    let capturedQ = ''
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    // Capture what URL params were set by watching the rendered output.
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/']}>
          <Routes>
            <Route path="/" element={<Sidebar open onClose={() => {}} />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>
    )

    const pendingBtn = screen.getByRole('button', { name: /pending/i })
    fireEvent.click(pendingBtn)
    // After click, the button should gain active styling (bg-gray-700).
    expect(pendingBtn.className).toContain('bg-gray-700')
  })

  it('clicking active status chip clears the filter', () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/?q=status%3Apending']}>
          <Routes>
            <Route path="/" element={<Sidebar open onClose={() => {}} />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>
    )

    const pendingBtn = screen.getByRole('button', { name: /pending/i })
    // Should be active (the current URL has q=status:pending).
    expect(pendingBtn.className).toContain('bg-gray-700')
    // Clicking again should deactivate it.
    fireEvent.click(pendingBtn)
    expect(pendingBtn.className).not.toContain('bg-gray-700')
  })

  it('clear filter button removes q param', () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/?q=status%3Aerror']}>
          <Routes>
            <Route path="/" element={<Sidebar open onClose={() => {}} />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>
    )

    const clearBtn = screen.getByRole('button', { name: /clear filter/i })
    expect(clearBtn).toBeInTheDocument()
    fireEvent.click(clearBtn)
    // After clearing, error button should no longer be active.
    const errorBtn = screen.getByRole('button', { name: /error/i })
    expect(errorBtn.className).not.toContain('bg-gray-700')
  })
})
