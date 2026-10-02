// The review demo (`pie --demo=review`, demo/apple-pie-review.tape): reviewers
// leave comments on a PR, the hub lists them, the agent fixes them locally,
// and the ticket parks at "fixes ready" for the human to approve.
//
// AND-207's worktree is a real (tiny) git repo, because the comments screen
// reads code context from it and the fixes-ready screen shows `git diff`: the
// scripted fix edits the files for real, so the diff on screen is genuine.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// reviewPR is AND-207's pull request, the one under review.
const reviewPR = 412

// reviewTicket is AND-207 with the log lines of its comment-fix run.
var reviewTicket = demoTicket{id: demoCrash.id, title: demoCrash.title, lines: map[string][]string{
	store.StateWorking: {
		"⚙ Read CheckoutViewModel.kt and the three review threads",
		"⚙ Edit CheckoutViewModel.kt - GlobalScope → viewModelScope",
		"⚙ Edit CheckoutViewModel.kt - fall back to Cart.EMPTY after process death",
		"⚙ Write CheckoutViewModelTest.kt - recreate the ViewModel mid-order",
		"⚙ Bash ./gradlew :app:testDebugUnitTest",
		"✓ CheckoutViewModelTest > recreatedMidOrder_doesNotCrash PASSED",
		"✓ BUILD SUCCESSFUL in 36s",
	},
}}

const (
	reviewVMPath   = "app/src/main/java/com/acme/shop/checkout/CheckoutViewModel.kt"
	reviewTestPath = "app/src/test/java/com/acme/shop/checkout/CheckoutViewModelTest.kt"
)

// reviewVM is the file on the PR. The comments' line numbers point into it.
const reviewVM = `package com.acme.shop.checkout

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import kotlinx.coroutines.GlobalScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch

class CheckoutViewModel(
    private val savedState: SavedStateHandle,
    private val payments: PaymentsRepository,
) : ViewModel() {

    private val cart: Cart = savedState.get<Cart>(KEY_CART)!!

    private val _state = MutableStateFlow(CheckoutState(cart))
    val state: StateFlow<CheckoutState> = _state

    fun placeOrder() {
        GlobalScope.launch {
            _state.value = _state.value.copy(placing = true)
            val result = payments.charge(cart.total)
            _state.value = _state.value.copy(placing = false, result = result)
        }
    }

    private companion object {
        const val KEY_CART = "cart"
    }
}
`

// reviewTest is the test the agent adds for the third comment.
const reviewTest = `package com.acme.shop.checkout

import androidx.lifecycle.SavedStateHandle
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertFalse
import org.junit.Test

class CheckoutViewModelTest {

    @Test
    fun recreatedMidOrder_doesNotCrash() = runTest {
        val saved = SavedStateHandle(mapOf("cart" to Cart.sample()))
        CheckoutViewModel(saved, FakePayments()).placeOrder()

        // Rotation: the old ViewModel is gone, a new one restores from state.
        val restored = CheckoutViewModel(saved, FakePayments())
        assertFalse(restored.state.value.placing)
    }
}
`

// reviewThreads are what the reviewers asked for, with the reply the agent
// drafts for each once it has fixed it.
var reviewThreads = []struct {
	th    review.Thread
	reply string
}{
	{review.Thread{ID: "PRRT_demo1", Kind: review.KindThread, Author: "maria-chen", Path: reviewVMPath, Line: 21,
		Body: "`GlobalScope` outlives the ViewModel, so rotating mid-payment leaks this job - and it can charge twice. Please use `viewModelScope`.",
		URL:  prURL(reviewPR) + "#discussion_r1", CommentCount: 1, LastCommentID: "c1"},
		"Switched to `viewModelScope`, so the charge is cancelled with the ViewModel instead of outliving a rotation."},
	{review.Thread{ID: "PRRT_demo2", Kind: review.KindThread, Author: "coderabbitai", Bot: true, Path: reviewVMPath, Line: 15,
		Body: "`!!` will crash after process death, when `SavedStateHandle` comes back without the cart. Fall back to an empty cart instead.",
		URL:  prURL(reviewPR) + "#discussion_r2", CommentCount: 1, LastCommentID: "c2"},
		"Replaced `!!` with a fallback to `Cart.EMPTY`; the screen reloads the cart when it comes back empty."},
	{review.Thread{ID: "PRRT_demo3", Kind: review.KindThread, Author: "maria-chen", Path: reviewVMPath, Line: 20,
		Body: "Can we get a test that recreates the ViewModel mid-order? That is exactly the crash from the ticket.",
		URL:  prURL(reviewPR) + "#discussion_r3", CommentCount: 1, LastCommentID: "c3"},
		"Added `CheckoutViewModelTest.recreatedMidOrder_doesNotCrash`, which restores the ViewModel from saved state mid-order."},
}

var reviewDemo = demoScenario{
	seed:    reviewSeed,
	play:    reviewPlay,
	spawned: reviewSpawned,
}

// reviewSeed: AND-207's PR has three open review threads (NEEDS YOU), one PR
// is clean, and two agents are at work.
func reviewSeed(d *demoDriver) error {
	wt, err := d.claim(reviewTicket)
	if err != nil {
		return err
	}
	if err := reviewRepo(wt); err != nil {
		return err
	}
	for _, t := range []demoTicket{demoOffline, demoDark, demoSpanish} {
		wt, err := d.claim(t)
		if err != nil {
			return err
		}
		if err := stubGit(wt); err != nil {
			return err
		}
	}
	d.log(reviewTicket.id, "✓ PR opened - "+strings.TrimPrefix(prURL(reviewPR), "https://"))
	_ = d.st.SetFields(reviewTicket.id, "pie/"+reviewTicket.id, wt, prURL(reviewPR))
	ths := make([]review.Thread, len(reviewThreads))
	for i, r := range reviewThreads {
		ths[i] = r.th
	}
	if err := d.st.UpsertThreads(reviewTicket.id, prURL(reviewPR), ths, true); err != nil {
		return err
	}
	d.set(reviewTicket.id, store.StateReview)

	d.log(demoOffline.id, "✓ PR opened - "+strings.TrimPrefix(prURL(409), "https://"))
	_ = d.st.SetFields(demoOffline.id, "pie/"+demoOffline.id, paths.WorktreeFor(demoRepo, demoOffline.id), prURL(409))
	d.set(demoOffline.id, store.StateReview)
	d.set(demoDark.id, store.StateWorking)
	d.set(demoSpanish.id, store.StatePlanning)
	return nil
}

// reviewRepo makes wt a git repo on the PR's branch, with the reviewed file
// committed - the base the fixes-ready diff is read against.
func reviewRepo(wt string) error {
	files := map[string]string{reviewVMPath: reviewVM, ".gitignore": ".agent/\n"}
	for p, body := range files {
		if err := demoWrite(filepath.Join(wt, p), body); err != nil {
			return err
		}
	}
	return demoGit(wt, [][]string{
		{"init", "-q", "-b", "pie/" + reviewTicket.id},
		{"add", "-A"},
		{"commit", "-q", "-m", reviewTicket.title},
	})
}

// reviewPlay keeps the rest of the fleet moving while the human reads.
func reviewPlay(d *demoDriver) {
	d.after(
		step{3 * time.Second, func() { d.set(demoSpanish.id, store.StateWorking) }},
		step{6 * time.Second, func() { d.set(demoDark.id, store.StateBuilding) }},
		step{12 * time.Second, func() { d.set(demoDark.id, store.StateTesting) }},
	)
}

// reviewSpawned stands in for `pie run --address-comments` (the fix) and
// `pie run --ship-comments` (the approve).
func reviewSpawned(d *demoDriver, args []string) {
	switch {
	case slices.Contains(args, "--address-comments"):
		reviewFix(d)
	case slices.Contains(args, "--ship-comments"):
		reviewShip(d)
	}
}

// reviewFix is the comment-fix run: edit the worktree for real, draft a reply
// per thread, park at fix-review. Nothing is committed - that is the point of
// the "fixes ready" gate.
func reviewFix(d *demoDriver) {
	id := reviewTicket.id
	wt := paths.WorktreeFor(demoRepo, id)
	vm := filepath.Join(wt, reviewVMPath)
	edit := func(old, new string) func() {
		return func() {
			if b, err := os.ReadFile(vm); err == nil {
				_ = os.WriteFile(vm, []byte(strings.Replace(string(b), old, new, 1)), 0o644)
			}
		}
	}
	d.log(id, "🤖 addressing 3 review comments on PR #412")
	d.set(id, store.StateWorking)
	d.after(
		step{1400 * time.Millisecond, func() {
			edit("import kotlinx.coroutines.GlobalScope\n", "")()
			edit("import androidx.lifecycle.ViewModel\n",
				"import androidx.lifecycle.ViewModel\nimport androidx.lifecycle.viewModelScope\n")()
			edit("GlobalScope.launch", "viewModelScope.launch")()
		}},
		step{2100 * time.Millisecond, edit("savedState.get<Cart>(KEY_CART)!!", "savedState.get<Cart>(KEY_CART) ?: Cart.EMPTY")},
		step{2800 * time.Millisecond, func() { _ = demoWrite(filepath.Join(wt, reviewTestPath), reviewTest) }},
		step{5600 * time.Millisecond, func() {
			for _, r := range reviewThreads {
				_ = d.st.SetDraftReply(r.th.ID, r.reply)
			}
			d.log(id, "✓ 3 fixes ready - nothing is committed, pushed or posted until you approve")
			d.set(id, store.StateFixReview)
		}},
	)
}

// reviewShip is the approve: commit, push, reply on each thread, back to review.
func reviewShip(d *demoDriver) {
	id := reviewTicket.id
	wt := paths.WorktreeFor(demoRepo, id)
	d.set(id, store.StateWorking)
	d.after(step{1500 * time.Millisecond, func() {
		_ = demoGit(wt, [][]string{{"add", "-A"}, {"commit", "-q", "-m", "Address review comments"}})
		sha, _ := exec.Command("git", "-C", wt, "rev-parse", "--short", "HEAD").Output()
		queued, _ := d.st.QueuedComments(id)
		ids, replies := make([]string, 0, len(queued)), map[string]string{}
		for _, c := range queued {
			ids = append(ids, c.ID)
			replies[c.ID] = "demo-reply-" + c.ID // our reply's comment id, as GitHub would return it
		}
		_ = d.st.MarkAddressed(ids, strings.TrimSpace(string(sha)))
		_ = d.st.MarkReplied(replies)
		_ = d.st.ClearQueued(id)
		d.log(id, "⚙ git push origin pie/"+id)
		d.log(id, "✓ replied on 3 threads - PR #412 is back with the reviewers")
		d.set(id, store.StateReview)
	}})
}

// demoWrite writes body to p, creating its directory.
func demoWrite(p, body string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(body), 0o644)
}

// demoGit runs each command in dir with a fixed demo identity, stopping at the
// first failure.
func demoGit(dir string, cmds [][]string) error {
	for _, c := range cmds {
		args := append([]string{"-C", dir, "-c", "user.name=Apple Pie", "-c", "user.email=demo@applepie.invalid"}, c...)
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("demo: git %s: %w: %s", strings.Join(c, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}
