import type { Meta, StoryObj } from '@storybook/react'
import { Sidebar } from '../components/Sidebar'

const meta = {
  component: Sidebar,
  tags: ['ai-generated'],
} satisfies Meta<typeof Sidebar>

export default meta
type Story = StoryObj<typeof meta>

export const Closed: Story = {
  args: { open: false, onClose: () => {} },
}

export const Open: Story = {
  args: { open: true, onClose: () => {} },
}
