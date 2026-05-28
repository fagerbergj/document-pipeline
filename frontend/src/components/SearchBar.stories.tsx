import type { Meta, StoryObj } from '@storybook/react'
import { expect, waitFor } from 'storybook/test'
import SearchBar from '../components/SearchBar'

const meta = {
  component: SearchBar,
  tags: ['ai-generated'],
} satisfies Meta<typeof SearchBar>

export default meta
type Story = StoryObj<typeof meta>

export const SimpleSearch: Story = {
  args: { stages: ['ingest', 'transcribe'], series: ['Basics', 'Advanced'] },
  play: async ({ canvas, userEvent }) => {
    const input = canvas.getByPlaceholderText(/Search title/i)
    await userEvent.type(input, 'document')
    await waitFor(() => {
      expect(input).toHaveValue('document')
    })
  },
}

export const WithFilters: Story = {
  args: { stages: ['ingest', 'transcribe', 'summarize'], series: ['Basics', 'Advanced', 'Reference'] },
}

export const AdvancedMode: Story = {
  args: { stages: ['ingest'], series: [] },
}

export const CssCheck: Story = {
  args: { stages: [], series: [] },
  play: async ({ canvas }) => {
    const input = canvas.getByPlaceholderText(/Search title/i)
    await expect(window.getComputedStyle(input).borderColor).toBe('rgb(75, 85, 99)')
  },
}
