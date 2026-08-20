import { cleanup,fireEvent,render,screen,waitFor } from '@testing-library/react'
import { afterEach,describe,expect,it } from 'vitest'
import { ResizableRightPanel } from './ResizableRightPanel'

afterEach(()=>{cleanup();window.localStorage.clear()})

describe('ResizableRightPanel',()=>{
  it('supports keyboard resizing, reset, and restores each panel width',async()=>{
    Object.defineProperty(window,'innerWidth',{configurable:true,value:1200})
    const view=render(<ResizableRightPanel panelKey="ai" defaultWidth={400}><aside>AI</aside></ResizableRightPanel>)
    const separator=screen.getByRole('separator',{name:'우측 패널 너비 조절'})
    expect(separator.parentElement).toHaveStyle({width:'400px'})
    fireEvent.keyDown(separator,{key:'ArrowLeft'})
    expect(separator.parentElement).toHaveStyle({width:'416px'})
    await waitFor(()=>expect(window.localStorage.getItem('kanpic:right-panel-width:ai')).toBe('416'))

    view.unmount()
    render(<ResizableRightPanel panelKey="ai" defaultWidth={360}><aside>AI 다시 열기</aside></ResizableRightPanel>)
    const restored=screen.getByRole('separator',{name:'우측 패널 너비 조절'})
    expect(restored.parentElement).toHaveStyle({width:'416px'})
    fireEvent.doubleClick(restored)
    expect(restored.parentElement).toHaveStyle({width:'360px'})
  })
})
