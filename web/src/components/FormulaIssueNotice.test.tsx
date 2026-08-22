import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { FormulaIssueNotice } from './FormulaIssueNotice'

const issue={row:1,column:3,code:'#REF!',message:'formula contains #REF!'}

afterEach(()=>cleanup())

describe('FormulaIssueNotice', () => {
  it('says nothing when the edit broke nothing', () => {
    const {container}=render(<FormulaIssueNotice issues={[]} onOpen={()=>{}} onClose={()=>{}}/>)
    expect(container).toBeEmptyDOMElement()
  })

  // 몇 곳인지만 알려 주고 어디인지 말하지 않으면 찾아다녀야 한다.
  it('names the first broken cell and what happened to it', () => {
    render(<FormulaIssueNotice issues={[issue,{...issue,row:2}]} onOpen={()=>{}} onClose={()=>{}}/>)
    expect(screen.getByRole('status')).toHaveTextContent('수식 2곳이 오류가 되었습니다')
    expect(screen.getByRole('status')).toHaveTextContent('C1 #REF!')
    expect(screen.getByRole('status')).toHaveTextContent('가리키던 셀이 사라졌습니다')
  })

  it('leads to the cell it named', () => {
    const onOpen=vi.fn()
    render(<FormulaIssueNotice issues={[issue]} onOpen={onOpen} onClose={()=>{}}/>)
    fireEvent.click(screen.getByRole('button',{name:'보기'}))
    expect(onOpen).toHaveBeenCalledWith(issue)
  })
})
