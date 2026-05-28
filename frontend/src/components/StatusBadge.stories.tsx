import type { Meta, StoryObj } from '@storybook/react'
import { StatusBadge } from '../components/StatusBadge'

const meta = {
  component: StatusBadge,
  tags: ['ai-generated'],
} satisfies Meta<typeof StatusBadge>

export default meta
type Story = StoryObj<typeof meta>

export const Pending: Story = {
  args: { state: 'pending' },
}

export const Running: Story = {
  args: { state: 'running' },
}

export const Done: Story = {
  args: { state: 'done' },
}
