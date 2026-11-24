package store

import (
	"path/filepath"
	"testing"
)

func TestOpenFillsTheScheduleOfLastRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hc.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := st.CreateSchedule(Schedule{ID: "sched0001", Title: "Standup", Prompt: "summarise", At: "08:00"})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []Task{{ID: "last0001", Title: "last", Source: "schedule"}, {ID: "old00001", Title: "old", Source: "schedule"},
		{ID: "hand0001", Title: "by hand"}} {
		if _, err := st.CreateTask(task, "you"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpdateSchedule(sc.ID, Fields{"last_task": "last0001"}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	if st, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for id, want := range map[string]string{"last0001": "sched0001", "old00001": "", "hand0001": ""} {
		if task, err := st.Task(id); err != nil || task.Schedule != want {
			t.Errorf("task %s has the schedule %q, want %q (%v)", id, task.Schedule, want, err)
		}
	}
}

func TestOpenHidesTheTasksDoneBeforeTheHiddenColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hc.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []Task{{ID: "done0001", Title: "done", State: "done"}, {ID: "open0001", Title: "open", State: "running"}} {
		if _, err := st.CreateTask(task, "you"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Exec("ALTER TABLE tasks DROP COLUMN hidden"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// The board hid a done task on its own before: it stays hidden.
	if st, err = Open(path); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"done0001": true, "open0001": false} {
		if task, err := st.Task(id); err != nil || task.Hidden != want {
			t.Errorf("task %s hidden %v, want %v (%v)", id, task.Hidden, want, err)
		}
	}
	if err := st.HideTask("done0001", false); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Only once: a task shown again stays in sight at the next open.
	if st, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if task, _ := st.Task("done0001"); task.Hidden {
		t.Error("a task shown again is hidden at the next open")
	}
}

// A task keeps its newest messages only, and loses them all when it is deleted.
func TestMessagesAreTrimmedAndDeleted(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateTask(Task{ID: "t1", Title: "t"}, "you"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < messagesKept+2; i++ {
		if err := st.AddMessage(Message{TaskID: "t1", Sender: "task", SenderTask: "t2", Kind: "message", Text: "hi"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddMessage(Message{TaskID: "t3", Sender: "you", Kind: "message", Text: "other"}); err != nil {
		t.Fatal(err)
	}
	kept, err := st.Messages("t1")
	if err != nil || len(kept) != messagesKept || kept[0].ID != 3 || kept[0].SenderTask != "t2" || kept[0].At == 0 {
		t.Fatalf("kept %d, first %+v, %v", len(kept), kept[0], err)
	}
	if err := st.DeleteTask("t1"); err != nil {
		t.Fatal(err)
	}
	if kept, _ = st.Messages("t1"); len(kept) != 0 {
		t.Errorf("a deleted task keeps %d messages", len(kept))
	}
	if other, _ := st.Messages("t3"); len(other) != 1 {
		t.Errorf("another task lost its messages: %v", other)
	}
}
