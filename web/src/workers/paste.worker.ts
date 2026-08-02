/// <reference lib="webworker" />
import { materializePaste } from '../lib/clipboard'

self.onmessage = (event: MessageEvent<{text:string;internal?:string;startRow:number;startColumn:number;valuesOnly?:boolean}>) => {
  try{
    self.postMessage({cells:materializePaste(event.data.text,event.data.internal,event.data.startRow,event.data.startColumn,event.data.valuesOnly)})
  }catch(error){
    self.postMessage({error:error instanceof Error?error.message:'붙여넣기 데이터를 처리하지 못했습니다.'})
  }
}
