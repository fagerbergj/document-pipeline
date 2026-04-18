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

function renderSidebar(initialPath = '/') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route path="/" element={<Sidebar open onClose={() => {}} />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('Sidebar filter chips', () => {
  it('renders status filter buttons', () => {
    renderSidebar()
    expect(screen.getByRole('button', { name: /pending/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /running/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /done/i })).toBeInTheDocument()
  })

  it('clicking a status chip activates it', () => {
    renderSidebar()
    const pendingBtn = screen.getByRole('button', { name: /pending/i })
    fireEvent.click(pendingBtn)
    expect(pendingBtn.className).toContain('bg-gray-700')
  })

  it('clicking the active status chip deactivates it', () => {
    renderSidebar('/?q=status%3Apending')
    const pendingBtn = screen.getByRole('button', { name: /pending/i })
    expect(pendingBtn.className).toContain('bg-gray-700')
    fireEvent.click(pendingBtn)
    expect(pendingBtn.className).not.toContain('bg-gray-700')
  })

  it('clear filter button removes the active filter', () => {
    renderSidebar('/?q=status%3Aerror')
    const clearBtn = screen.getByRole('button', { name: /clear filter/i })
    expect(clearBtn).toBeInTheDocument()
    fireEvent.click(clearBtn)
    const errorBtn = screen.getByRole('button', { name: /error/i })
    expect(errorBtn.className).not.toContain('bg-gray-700')
  })
})
