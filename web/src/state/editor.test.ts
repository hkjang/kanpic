import { beforeEach, describe, expect, it } from 'vitest'
import { useEditorStore } from './editor'

beforeEach(() => useEditorStore.getState().reset())

describe('editor operation history', () => {
  it('moves acknowledged operations through undo and redo stacks', () => {
    useEditorStore.getState().recordOperation('operation-1')
    useEditorStore.getState().recordOperation('operation-2')

    expect(useEditorStore.getState().takeUndo()).toBe('operation-2')
    useEditorStore.getState().completeUndo('undo-operation-2')
    expect(useEditorStore.getState().redoStack).toEqual(['undo-operation-2'])

    expect(useEditorStore.getState().takeRedo()).toBe('undo-operation-2')
    useEditorStore.getState().completeRedo('redo-operation-2')
    expect(useEditorStore.getState().undoStack).toEqual(['operation-1', 'redo-operation-2'])
  })

  it('clears redo history when a new operation is acknowledged', () => {
    useEditorStore.getState().recordOperation('operation-1')
    useEditorStore.getState().takeUndo()
    useEditorStore.getState().completeUndo('undo-operation-1')
    useEditorStore.getState().recordOperation('operation-new')

    expect(useEditorStore.getState().redoStack).toEqual([])
    expect(useEditorStore.getState().undoStack).toEqual(['operation-new'])
  })
})
