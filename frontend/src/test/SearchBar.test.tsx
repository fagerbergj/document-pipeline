import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import SearchBar, { buildLuceneQuery } from '../components/SearchBar'

// ── buildLuceneQuery unit tests ───────────────────────────────────────────────

describe('buildLuceneQuery', () => {
  it('returns empty string when nothing is set', () => {
    expect(buildLuceneQuery('', '', '')).toBe('')
  })

  it('wraps single-word text search in title/content OR', () => {
    expect(buildLuceneQuery('meeting', '', '')).toBe('(title:meeting OR content:meeting)')
  })

  it('quotes multi-word text search', () => {
    expect(buildLuceneQuery('quarterly review', '', '')).toBe('(title:"quarterly review" OR content:"quarterly review")')
  })

  it('adds status filter', () => {
    expect(buildLuceneQuery('', 'pending', '')).toBe('status:pending')
  })

  it('adds stage filter', () => {
    expect(buildLuceneQuery('', '', 'ocr')).toBe('stage:ocr')
  })

  it('combines all parts with AND', () => {
    const q = buildLuceneQuery('meeting', 'pending', 'ocr')
    expect(q).toBe('(title:meeting OR content:meeting) AND status:pending AND stage:ocr')
  })

  it('omits empty parts', () => {
    expect(buildLuceneQuery('', 'done', '')).toBe('status:done')
    expect(buildLuceneQuery('notes', '', 'classify')).toBe('(title:notes OR content:notes) AND stage:classify')
  })
})

// ── SearchBar component tests ─────────────────────────────────────────────────

function renderSearchBar(initialPath = '/') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route path="/" element={<SearchBar stages={['ocr', 'clarify', 'classify', 'embed']} />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('SearchBar', () => {
  it('renders text input and dropdowns', () => {
    renderSearchBar()
    expect(screen.getByPlaceholderText(/search title/i)).toBeInTheDocument()
    expect(screen.getAllByRole('combobox').length).toBeGreaterThanOrEqual(2) // status + stage selects
  })

  it('renders all pipeline stages in stage dropdown', () => {
    renderSearchBar()
    expect(screen.getByRole('option', { name: 'ocr' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'classify' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'embed' })).toBeInTheDocument()
  })

  it('renders all status options', () => {
    renderSearchBar()
    expect(screen.getByRole('option', { name: 'pending' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'done' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'error' })).toBeInTheDocument()
  })

  it('shows active status pill when status param is set', () => {
    renderSearchBar('/?status=pending')
    expect(screen.getByText(/status: pending/i)).toBeInTheDocument()
  })

  it('shows active stage pill when stage param is set', () => {
    renderSearchBar('/?stage=ocr')
    expect(screen.getByText(/stage: ocr/i)).toBeInTheDocument()
  })

  it('pill dismiss button removes the filter', () => {
    renderSearchBar('/?status=error')
    const dismissBtn = screen.getByRole('button', { name: '×' })
    fireEvent.click(dismissBtn)
    expect(screen.queryByText(/status: error/i)).not.toBeInTheDocument()
  })

  it('advanced toggle button is present', () => {
    renderSearchBar()
    expect(screen.getByRole('button', { name: /advanced/i })).toBeInTheDocument()
  })

  it('shows advanced input when adv param is set', () => {
    renderSearchBar('/?adv=status%3Apending')
    expect(screen.getByPlaceholderText(/status:pending/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /simple/i })).toBeInTheDocument()
  })

  it('switching to advanced mode shows the assembled query', () => {
    renderSearchBar('/?status=pending&stage=ocr')
    fireEvent.click(screen.getByRole('button', { name: /advanced/i }))
    // After clicking Advanced, input should contain the assembled query
    const input = screen.getByPlaceholderText(/status:pending/i) as HTMLInputElement
    expect(input.value).toBe('status:pending AND stage:ocr')
  })
})
