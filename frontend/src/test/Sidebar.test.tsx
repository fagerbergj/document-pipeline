import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Sidebar from '../components/Sidebar'

vi.mock('../api', () => ({
  api: {
    pipeline: () => Promise.resolve({ stages: [{ name: 'ocr' }, { name: 'classify' }] }),
    jobs: () => Promise.resolve({
      data: [
        { id: 'j1', document_id: 'd1', stage: 'ocr',      status: 'pending',  updated_at: '2024-01-01T00:00:00Z' },
        { id: 'j2', document_id: 'd2', stage: 'classify',  status: 'done',     updated_at: '2024-01-01T00:00:00Z' },
        { id: 'j3', document_id: 'd3', stage: 'ocr',       status: 'error',    updated_at: '2024-01-01T00:00:00Z' },
      ],
    }),
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

describe('Sidebar counts', () => {
  it('renders nav links', () => {
    renderSidebar()
    expect(screen.getByRole('link', { name: /dashboard/i })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /contexts/i })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /chat/i })).toBeInTheDocument()
  })

  it('renders status labels as read-only text', () => {
    renderSidebar()
    expect(screen.getByText('pending')).toBeInTheDocument()
    expect(screen.getByText('done')).toBeInTheDocument()
    expect(screen.getByText('error')).toBeInTheDocument()
  })

  it('has no clickable filter chips for status', () => {
    renderSidebar()
    // Status items are divs, not buttons — no role="button" for status rows
    const buttons = screen.queryAllByRole('button')
    const statusButtons = buttons.filter(b => /pending|running|waiting|error|done/.test(b.textContent ?? ''))
    expect(statusButtons).toHaveLength(0)
  })

  it('does not show clear filter button', () => {
    renderSidebar('/?status=pending')
    expect(screen.queryByRole('button', { name: /clear filter/i })).not.toBeInTheDocument()
  })
})
