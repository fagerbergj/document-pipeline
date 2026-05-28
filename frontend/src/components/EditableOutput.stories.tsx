import type { Meta, StoryObj } from '@storybook/react'
import EditableOutput from '../components/EditableOutput'

const meta = {
  component: EditableOutput,
  tags: ['ai-generated'],
} satisfies Meta<typeof EditableOutput>

export default meta
type Story = StoryObj<typeof meta>

export const TextView: Story = {
  args: {
    field: 'clarified_text',
    content: '# Heading\n\nThis is **bold** text.',
  },
}

export const EditMode: Story = {
  args: {
    field: 'clarified_text',
    content: 'Editable content',
    editing: true,
    onChange: () => {},
  },
}

export const RawText: Story = {
  args: {
    field: 'raw_text',
    content: 'Raw monospaced content\nwith multiple lines',
  },
}
