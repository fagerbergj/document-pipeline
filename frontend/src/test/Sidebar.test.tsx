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

  it('renders status rows as filter links', () => {
    renderSidebar()
    expect(screen.getByRole('link', { name: /pending/i })).toHaveAttribute('href', '/?status=pending')
    expect(screen.getByRole('link', { name: /done/i })).toHaveAttribute('href', '/?status=done')
  })

  it('toggles the active status filter off when re-clicked', () => {
    renderSidebar('/?status=pending')
    // Already pending → link should clear the param
    expect(screen.getByRole('link', { name: /pending/i })).toHaveAttribute('href', '/')
    // Other statuses still set their own
    expect(screen.getByRole('link', { name: /done/i })).toHaveAttribute('href', '/?status=done')
  })
})
