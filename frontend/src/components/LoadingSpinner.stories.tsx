import type { Meta, StoryObj } from '@storybook/react'
import { LoadingSpinner } from '../components/LoadingSpinner'

const meta = {
  component: LoadingSpinner,
  tags: ['ai-generated'],
} satisfies Meta<typeof LoadingSpinner>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {}
