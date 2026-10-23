Feature: Void a label
  Voiding closes a LABELLED journey as VOIDED. It is refused once the package
  was handed over to the network, and repeating it changes nothing.

  Background:
    Given customer order "ord-1" ships to a synthetic address
    And a label has been issued for package 1 of "PO-1"

  Scenario: Void before handover
    When I void the label with reason "order cancelled"
    Then the request succeeds
    And the label has carrier "RN-EXPRESS" and status "VOIDED"
    And the journey is "VOIDED"
    And the published events are "LabelIssued,LabelVoided"

  Scenario: Voiding twice returns the label unchanged
    Given I void the label with reason "first"
    When I void the label with reason "second"
    Then the request succeeds
    And the published events are "LabelIssued,LabelVoided"

  Scenario: A voided package cannot be scanned
    Given I void the label with reason "order cancelled"
    When I record a "PICKUP_DEPARTURE" scan "s1" at hour 1
    Then the request is refused with "journey-closed"

  Scenario: Void after handover is refused
    Given I record a "PICKUP_DEPARTURE" scan "s1" at hour 1
    When I void the label with reason "too late"
    Then the request is refused with "void-after-handover"
    And the journey is "HANDED_OVER"
